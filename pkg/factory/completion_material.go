// Frozen D13 completion material (FW32 material cutover, SUPERVISOR-077).
//
// CompletionMaterial is the canonical MODEL-SAFE local context sealed
// alongside the native inventory: repository plus complete non-nil ordered
// read projections over the accepted scope, preparations, plans, active
// work, delivery records, check verdicts and authority. Empty queried
// sections are explicit empty arrays; nil/missing sections are incomplete
// and refuse. No command/state machines live here: the new types are safe
// READ projections reusing factory.Acceptance, factory.IssueControl,
// factory.InvestigationPlan, factory.IntentTaskBinding, factory.IntentTask
// and factory.ProductIdea. No prompts, terminals, credential/broker paths,
// connection secrets, setup variable values, raw mutation payloads or
// receipts, or private draft bodies enter any projection.
package factory

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/levitateos/factory/pkg/project"
)

// MaxCompletionMaterialBytes bounds the complete encoded material: the
// existing sealed-evidence byte bound. Oversize material refuses whole,
// never truncated, never sharded to an artifact path or URL.
const MaxCompletionMaterialBytes = MaxReassessmentEvidenceBytes

// CompletionIntentScope is one admitted historical accepted revision with
// its effective scope and delivery bindings. Only effective approved ideas
// (IDs in EffectiveOutcomes, or accepted/edited choices when no effective
// subset was recorded) and authorized tasks are exported: unaccepted
// ideas and private successor bodies never enter. LatestAccepted marks the
// newest accepted revision on the actual immutable predecessor chain from
// the current head; CurrentState names the head state as enum metadata
// only and never contributes head content. Private successor head IDs and
// content are never exported. Generation fields come from the stored
// intent generation when the revision was submitted; raw generation
// payload/receipt bytes are never exported. Binding CreatedUnix is
// normalized away: bindings carry identity, not observation time.
type CompletionIntentScope struct {
	Bindings          []IntentTaskBinding               `json:"bindings"`
	SubmittedTasks    []IntentTask                      `json:"submitted_tasks,omitempty"`
	Delivered         map[string]GeneratedIssueIdentity `json:"delivered,omitempty"`
	Ideas             []ProductIdea                     `json:"ideas"`
	Tasks             []IntentTask                      `json:"tasks"`
	EffectiveOutcomes []string                          `json:"effective_outcomes,omitempty"`
	IntentID          string                            `json:"intent_id"`
	RevisionID        string                            `json:"revision_id"`
	RevisionDigest    string                            `json:"revision_digest"`
	CurrentState      string                            `json:"current_state"`
	Objective         string                            `json:"objective"`
	Approach          string                            `json:"approach,omitempty"`
	GenerationState   string                            `json:"generation_state,omitempty"`
	OperationID       string                            `json:"operation_id,omitempty"`
	LatestAccepted    bool                              `json:"latest_accepted,omitempty"`
	LocalOnly         bool                              `json:"local_only,omitempty"`
	Repository        int64                             `json:"repository,string"`
}

// validCompletionIntentState reports whether state is known intent
// revision metadata. There is no "reviewed" state.
func validCompletionIntentState(state string) bool {
	switch state {
	case "draft", "accepted", "declined", "rejected", "abandoned":
		return true
	default:
		return false
	}
}

// Validate checks one scope entry: identities, the effective filter that
// produced its ideas/tasks, generation/binding/delivered consistency and
// digest shape. Cross-entry references (binding acceptances inside the
// material acceptance set) are checked by CompletionMaterial.Validate.
func (s CompletionIntentScope) validateIdentity() (map[string]bool, error) {
	if !project.ValidDecisionID(s.IntentID) || !project.ValidDecisionID(s.RevisionID) {
		return nil, errors.New("invalid completion scope identity")
	}
	if s.Repository <= 0 {
		return nil, errors.New("invalid completion scope repository")
	}
	if !ValidDigest(s.RevisionDigest) {
		return nil, errors.New("invalid completion scope revision digest")
	}
	if !validCompletionIntentState(s.CurrentState) {
		return nil, errors.New("invalid completion scope head state")
	}
	if !intentText(s.Objective, 16000) {
		return nil, errors.New("invalid completion scope objective")
	}
	if s.Approach != "" && s.Approach != "incremental" && s.Approach != "complete_first" {
		return nil, errors.New("invalid completion scope approach")
	}
	effective := make(map[string]bool, len(s.EffectiveOutcomes))
	for _, id := range s.EffectiveOutcomes {
		if !project.ValidDecisionID(id) || effective[id] {
			return nil, errors.New("invalid completion scope effective outcome")
		}
		effective[id] = true
	}
	return effective, nil
}

func (s CompletionIntentScope) validateIdeas(effective map[string]bool) (map[string]bool, error) {
	if s.Ideas == nil {
		return nil, errors.New("completion scope lacks its ideas")
	}
	kept := make(map[string]bool, len(s.Ideas))
	for _, idea := range s.Ideas {
		if !project.ValidDecisionID(idea.ID) || !intentText(idea.Description, 4000) ||
			!intentText(idea.Consequence, 4000) {
			return nil, errors.New("invalid completion scope idea")
		}
		if kept[idea.ID] {
			return nil, errors.New("duplicate completion scope idea")
		}
		kept[idea.ID] = true
		if idea.Decision != "accept" && idea.Decision != "edit" {
			return nil, errors.New("completion scope idea is not an authorized choice")
		}
		if len(effective) > 0 && !effective[idea.ID] {
			return nil, errors.New("completion scope idea exceeds its effective outcomes")
		}
	}
	return kept, nil
}

func (s CompletionIntentScope) validateTasks(kept map[string]bool) (map[string]IntentTask, error) {
	if len(s.Tasks) == 0 {
		return nil, errors.New("completion scope lacks its authorized tasks")
	}
	tasks := make(map[string]IntentTask, len(s.Tasks))
	for _, task := range s.Tasks {
		if !project.ValidDecisionID(task.Key) || !intentText(task.Objective, 4000) ||
			len(task.Outcomes) == 0 || len(task.IdeaIDs) == 0 {
			return nil, errors.New("invalid completion scope task")
		}
		if _, dup := tasks[task.Key]; dup {
			return nil, errors.New("duplicate completion scope task")
		}
		for _, id := range task.IdeaIDs {
			if !kept[id] {
				return nil, errors.New("completion scope task exceeds its exported ideas")
			}
		}
		tasks[task.Key] = task
	}
	return tasks, nil
}

func (s CompletionIntentScope) validateSubmitted(kept map[string]bool, tasks map[string]IntentTask) (map[string]bool, error) {
	// Submitted tasks are validated against the same exported
	// authority as authorized tasks: shape, exported effective ideas,
	// and dependency existence. The constructor additionally proves
	// the whole submitted graph through IntentRevision.Validate, so
	// persisted/direct DTOs cannot fund or use an unaccepted graph.
	submitted := make(map[string]bool, len(s.SubmittedTasks))
	for _, task := range s.SubmittedTasks {
		if !project.ValidDecisionID(task.Key) || !intentText(task.Objective, 4000) ||
			len(task.Outcomes) == 0 || len(task.IdeaIDs) == 0 {
			return nil, errors.New("invalid completion scope submitted task")
		}
		if submitted[task.Key] {
			return nil, errors.New("duplicate completion scope submitted task")
		}
		for _, id := range task.IdeaIDs {
			if !kept[id] {
				return nil, errors.New("completion scope submitted task exceeds its exported ideas")
			}
		}
		submitted[task.Key] = true
	}
	known := make(map[string]bool, len(tasks)+len(submitted))
	for key := range tasks {
		known[key] = true
	}
	for key := range submitted {
		known[key] = true
	}
	graph := make(map[string][]string, len(s.SubmittedTasks))
	for _, task := range s.SubmittedTasks {
		for _, dep := range task.DependsOn {
			if !known[dep] {
				return nil, errors.New("completion scope submitted task depends on an unexported task")
			}
		}
		graph[task.Key] = task.DependsOn
	}
	if err := checkSubmittedAcyclic(graph); err != nil {
		return nil, err
	}
	return submitted, nil
}

func (s CompletionIntentScope) validateGenerationState() error {
	if s.OperationID == "" {
		if s.LocalOnly && s.GenerationState == "completed" && len(s.SubmittedTasks) > 0 && len(s.Bindings) == len(s.SubmittedTasks) {
			return nil
		}
		if s.GenerationState != "" || len(s.SubmittedTasks) != 0 || len(s.Delivered) != 0 {
			return errors.New("completion scope generation lacks its operation")
		}
	} else {
		if s.GenerationState == "" || len(s.GenerationState) > 64 ||
			strings.IndexFunc(s.GenerationState, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
			return errors.New("invalid completion scope generation state")
		}
		if len(s.SubmittedTasks) == 0 {
			return errors.New("completion scope generation lacks its submitted tasks")
		}
	}
	return nil
}

func (s CompletionIntentScope) validateDeliveredBindings(submitted map[string]bool, tasks map[string]IntentTask) error {
	for key, ref := range s.Delivered {
		if !submitted[key] {
			return errors.New("completion scope delivered names an unsubmitted task")
		}
		if ref.IssueID <= 0 || ref.IssueIndex <= 0 || ref.ContentVersion < 0 {
			return errors.New("invalid completion scope delivered ref")
		}
	}
	if s.Bindings == nil {
		return errors.New("completion scope lacks its task bindings")
	}
	for _, binding := range s.Bindings {
		if binding.RevisionID != s.RevisionID {
			return errors.New("completion scope binding names another revision")
		}
		if !project.ValidDecisionID(binding.TaskKey) || binding.IssueID <= 0 ||
			binding.IssueIndex <= 0 || binding.ContentVersion < 0 ||
			!project.ValidDecisionID(binding.AcceptanceID) || binding.CreatedUnix != 0 {
			return errors.New("invalid completion scope task binding")
		}
		if _, ok := tasks[binding.TaskKey]; !ok && !submitted[binding.TaskKey] {
			return errors.New("completion scope binding names an unexported task")
		}
	}
	return nil
}

func (s CompletionIntentScope) validateGeneration(submitted map[string]bool, tasks map[string]IntentTask) error {
	if err := s.validateGenerationState(); err != nil {
		return err
	}
	return s.validateDeliveredBindings(submitted, tasks)
}

func (s CompletionIntentScope) Validate() error {
	effective, err := s.validateIdentity()
	if err != nil {
		return err
	}
	kept, err := s.validateIdeas(effective)
	if err != nil {
		return err
	}
	tasks, err := s.validateTasks(kept)
	if err != nil {
		return err
	}
	submitted, err := s.validateSubmitted(kept, tasks)
	if err != nil {
		return err
	}
	return s.validateGeneration(submitted, tasks)
}

// checkSubmittedAcyclic refuses cyclic dependencies among submitted
// tasks. Deps were already proven to name exported tasks; this closes
// the remaining malformed-graph shape on persisted/direct DTOs.
func checkSubmittedAcyclic(graph map[string][]string) error {
	visited, active := map[string]bool{}, map[string]bool{}
	var visit func(string) error
	visit = func(key string) error {
		if active[key] {
			return errors.New("cyclic completion scope submitted tasks")
		}
		if visited[key] {
			return nil
		}
		active[key] = true
		for _, dep := range graph[key] {
			if _, ok := graph[dep]; ok {
				if err := visit(dep); err != nil {
					return err
				}
			}
		}
		active[key] = false
		visited[key] = true
		return nil
	}
	for key := range graph {
		if err := visit(key); err != nil {
			return err
		}
	}
	return nil
}

// NewCompletionIntentScope projects one admitted accepted revision: the
// effective idea/task filter, the revision digest over the complete stored
// revision, and the stored generation/bindings with observation time
// normalized away. The caller supplies the head state and whether this
// revision is the newest accepted on the head predecessor chain.
func admitScopeRevision(revision IntentRevision, headState string) error {
	if err := revision.Validate(); err != nil {
		return err
	}
	if revision.State != "accepted" {
		return errors.New("completion scope needs its accepted revision")
	}
	if !validCompletionIntentState(headState) {
		return errors.New("invalid completion scope head state")
	}
	return nil
}

func exportScopeIdeas(revision IntentRevision, scope *CompletionIntentScope) {
	effective := make(map[string]bool, len(revision.EffectiveOutcomes))
	for _, id := range revision.EffectiveOutcomes {
		effective[id] = true
	}
	for _, idea := range revision.Ideas {
		// Current authorized-choice semantics: effective members when an
		// effective subset is recorded, else accepted/edited choices.
		// Pending and declined ideas never export.
		if idea.Decision != "accept" && idea.Decision != "edit" {
			continue
		}
		if len(effective) > 0 && !effective[idea.ID] {
			continue
		}
		scope.Ideas = append(scope.Ideas, idea)
	}
	kept := make(map[string]bool, len(scope.Ideas))
	for _, idea := range scope.Ideas {
		kept[idea.ID] = true
	}
	for _, task := range revision.Tasks {
		authorized := true
		for _, id := range task.IdeaIDs {
			if !kept[id] {
				authorized = false
				break
			}
		}
		if authorized {
			scope.Tasks = append(scope.Tasks, task)
		}
	}
}

func admitScopeGeneration(revision IntentRevision, generation *IntentGeneration, scope *CompletionIntentScope) error {
	if generation.RevisionID != revision.ID || generation.IntentID != revision.Intent {
		return errors.New("completion scope generation names another revision")
	}
	if generation.IntentDigest != revision.Digest() {
		return errors.New("completion scope generation names another revision digest")
	}
	// Authorize the submitted graph against the current
	// accepted/effective idea authority: the accepted revision
	// carrying the submitted tasks must validate under the
	// accepted-state rules, so every submitted task names
	// exported effective accepted/edit choices with valid
	// text, outcomes, IDs, and dependency DAG. Derived
	// keys/objectives are allowed; unaccepted, unknown, or
	// pending ideas refuse the WHOLE capture — never filter.
	authorized := revision
	authorized.Tasks = append([]IntentTask(nil), generation.SubmittedTasks...)
	if err := authorized.Validate(); err != nil {
		return err
	}
	scope.GenerationState = generation.State
	scope.OperationID = generation.OperationID
	scope.LocalOnly = generation.LocalOnly
	scope.SubmittedTasks = append([]IntentTask(nil), generation.SubmittedTasks...)
	if len(generation.Delivered) > 0 {
		scope.Delivered = make(map[string]GeneratedIssueIdentity, len(generation.Delivered))
		for key, ref := range generation.Delivered {
			scope.Delivered[key] = ref
		}
	}
	return nil
}

func NewCompletionIntentScope(revision IntentRevision, headState string, latestAccepted bool, generation *IntentGeneration, bindings []IntentTaskBinding) (CompletionIntentScope, error) {
	if err := admitScopeRevision(revision, headState); err != nil {
		return CompletionIntentScope{}, err
	}
	scope := CompletionIntentScope{
		Bindings: make([]IntentTaskBinding, 0, len(bindings)),
		Ideas:    []ProductIdea{},
		Tasks:    []IntentTask{},
		IntentID: revision.Intent, RevisionID: revision.ID,
		RevisionDigest: revision.Digest(), CurrentState: headState,
		Objective: revision.Objective, Approach: revision.Approach,
		EffectiveOutcomes: append([]string(nil), revision.EffectiveOutcomes...),
		LatestAccepted:    latestAccepted, Repository: revision.Repository,
	}
	exportScopeIdeas(revision, &scope)
	if generation != nil {
		if err := admitScopeGeneration(revision, generation, &scope); err != nil {
			return CompletionIntentScope{}, err
		}
	}
	for _, binding := range bindings {
		binding.CreatedUnix = 0
		scope.Bindings = append(scope.Bindings, binding)
	}
	if err := scope.Validate(); err != nil {
		return CompletionIntentScope{}, err
	}
	return scope, nil
}

// CompletionWithdrawal is one acceptance withdrawal reference: the issue
// plus the withdrawn acceptance ID. Bodies live in the acceptances
// section; this section carries references only.
type CompletionWithdrawal struct {
	Issue        int64  `json:"issue,string"`
	AcceptanceID string `json:"acceptance_id"`
}

func (w CompletionWithdrawal) Validate() error {
	if w.Issue <= 0 || !project.ValidDecisionID(w.AcceptanceID) {
		return errors.New("invalid completion withdrawal reference")
	}
	return nil
}

// CompletionPreparation is the standing preparation binding for one
// project role: exact identity/source/requirement/approval digests plus
// the Ready verdict and Phase. No setup maps, config values, tools,
// credentials or container custody enter.
type CompletionPreparation struct {
	RequirementDigest string `json:"requirement_digest"`
	ApprovalDigest    string `json:"approval_digest"`
	SetupDigest       string `json:"setup_digest"`
	SourceCommit      string `json:"source_commit"`
	ID                string `json:"id"`
	Role              string `json:"role"`
	Phase             string `json:"phase,omitempty"`
	Revision          int64  `json:"revision"`
	Ready             bool   `json:"ready"`
}

func (p CompletionPreparation) Validate() error {
	if !project.ValidPreparationID(p.ID) || !project.ValidFactoryRole(p.Role) || p.Revision < 0 {
		return errors.New("invalid completion preparation identity")
	}
	if !ValidCommit(p.SourceCommit) || !ValidDigest(p.SetupDigest) ||
		!ValidDigest(p.RequirementDigest) || !ValidDigest(p.ApprovalDigest) {
		return errors.New("invalid completion preparation digests")
	}
	if p.Phase != "" && !project.ValidPreparePhase(p.Phase) {
		return errors.New("invalid completion preparation phase")
	}
	return nil
}

// CompletionPlanHead is one retained plan pointer: attempt root, plan and
// generation. Revisions live in the plans section; this section carries
// pointers only.
type CompletionPlanHead struct {
	AttemptRoot string `json:"attempt_root"`
	PlanID      string `json:"plan_id"`
	Generation  int64  `json:"generation"`
}

func (h CompletionPlanHead) Validate() error {
	if !ValidID(h.AttemptRoot) || !ValidID(h.PlanID) || h.Generation < 0 {
		return errors.New("invalid completion plan head")
	}
	return nil
}

// completionPlanTaskStates mirrors the stored receipt states. A pending
// task has no child; every other live or terminal state carries its
// native child identity except a cancelled task that never launched.
func validCompletionPlanTaskState(state string) bool {
	switch state {
	case "pending", "running", "output_ready", "completed", "failed", "cancelled":
		return true
	default:
		return false
	}
}

// CompletionPlanTask projects one validated plan task receipt: root, plan
// and task IDs, revision, state, child, input/output digests, tree/commit
// evidence and check refs. No event sequences, reason strings or logs.
type CompletionPlanTask struct {
	Checks       []string `json:"checks"`
	InputDigest  string   `json:"input_digest,omitempty"`
	OutputDigest string   `json:"output_digest,omitempty"`
	Tree         string   `json:"tree,omitempty"`
	Commit       string   `json:"commit,omitempty"`
	AttemptRoot  string   `json:"attempt_root"`
	PlanID       string   `json:"plan_id"`
	TaskID       string   `json:"task_id"`
	Revision     int64    `json:"revision"`
	State        string   `json:"state"`
	Child        string   `json:"child,omitempty"`
}

func (t CompletionPlanTask) validateIdentity() error {
	if !ValidID(t.AttemptRoot) || !ValidID(t.PlanID) || !ValidPlanKey(t.TaskID) {
		return errors.New("invalid completion plan task identity")
	}
	if t.Revision < 0 || !validCompletionPlanTaskState(t.State) {
		return errors.New("invalid completion plan task revision or state")
	}
	if t.State == "pending" {
		if t.Child != "" {
			return errors.New("pending completion plan task has no child")
		}
	} else if len(t.Child) > 128 || (t.Child == "" && t.State != "cancelled") {
		return errors.New("completion plan task child outside its bound")
	}
	return nil
}

func (t CompletionPlanTask) validateDigests() error {
	if t.InputDigest != "" && !ValidDigest(t.InputDigest) {
		return errors.New("invalid completion plan task input digest")
	}
	if t.OutputDigest != "" && !ValidDigest(t.OutputDigest) {
		return errors.New("invalid completion plan task output digest")
	}
	if (t.State == "output_ready" || t.State == "completed") && t.OutputDigest == "" {
		return errors.New("completion plan task output lacks its digest")
	}
	if t.Checks == nil {
		return errors.New("completion plan task lacks its check refs")
	}
	for _, id := range t.Checks {
		if !ValidPlanKey(id) {
			return errors.New("completion plan task check is not a plan check key")
		}
	}
	if t.Tree != "" && !ValidCommit(t.Tree) {
		return errors.New("completion plan task tree evidence lacks its identity")
	}
	if t.Commit != "" && !ValidCommit(t.Commit) {
		return errors.New("completion plan task commit evidence lacks its identity")
	}
	return nil
}

func (t CompletionPlanTask) Validate() error {
	if err := t.validateIdentity(); err != nil {
		return err
	}
	return t.validateDigests()
}

// CompletionRunState is one run-history entry: the run ID, its outcome,
// whether it reconciled, and the actual recorded active/settled flags.
// Settlement is never inferred from missing rows; the producer fails
// closed instead. No admission profile, credential, lease, binding or
// output data enters.
type CompletionRunState struct {
	ID         string  `json:"id"`
	Outcome    Outcome `json:"outcome,omitempty"`
	Reconciled bool    `json:"reconciled,omitempty"`
	Active     bool    `json:"active,omitempty"`
	Settled    bool    `json:"settled,omitempty"`
}

func (r CompletionRunState) Validate() error {
	if !ValidID(r.ID) || !validOutcome(r.Outcome) {
		return errors.New("invalid completion run state")
	}
	if r.Outcome == "" && r.Reconciled {
		return errors.New("reconciled completion run lacks its outcome")
	}
	return nil
}

// CompletionReservation is the held capacity behind one assignment: the
// connection, state, planned minutes and revision. The assignment key is
// the parent entry; no usage charges enter.
type CompletionReservation struct {
	Connection     string `json:"connection"`
	State          string `json:"state"`
	PlannedMinutes int    `json:"planned_minutes"`
	Revision       int64  `json:"revision"`
}

func (r CompletionReservation) Validate() error {
	if r.Connection == "" || len(r.Connection) > 128 {
		return errors.New("invalid completion reservation connection")
	}
	if !ValidReservationState(r.State) || r.Revision < 0 {
		return errors.New("invalid completion reservation state")
	}
	if r.PlannedMinutes < 1 || r.PlannedMinutes > 180 {
		return errors.New("invalid completion reservation plan")
	}
	return nil
}

// CompletionAssignment projects one repository-owned issue or intent
// assignment: exact identity, root, subject, issue, role, phase, stage,
// outcome, reason, source, preparation and revision, plus run-history
// states and reservation state. Assignment.Prompt, account/lease/binding/
// credential data and run output never enter. Completion-subject
// workflow roots are excluded: their own admitted run must not invalidate
// its pre-admission issue snapshot.
type CompletionAssignment struct {
	RunHistory   []CompletionRunState   `json:"run_history"`
	Reservation  *CompletionReservation `json:"reservation,omitempty"`
	Subject      ExecutionSubject       `json:"subject,omitempty"`
	ID           string                 `json:"id"`
	AttemptRoot  string                 `json:"attempt_root"`
	ProjectID    string                 `json:"project_id"`
	Role         string                 `json:"role"`
	Phase        string                 `json:"phase,omitempty"`
	Stage        string                 `json:"stage"`
	Outcome      Outcome                `json:"outcome,omitempty"`
	Reason       string                 `json:"reason,omitempty"`
	Acceptance   string                 `json:"acceptance,omitempty"`
	Preparation  string                 `json:"preparation"`
	SourceCommit string                 `json:"source_commit"`
	Issue        int64                  `json:"issue,string"`
	Revision     int64                  `json:"revision"`
}

func (a CompletionAssignment) validateAddress() error {
	if !ValidID(a.ID) || !ValidID(a.AttemptRoot) {
		return errors.New("invalid completion assignment identity")
	}
	if !ValidProjectID(a.ProjectID) || !project.ValidFactoryRole(a.Role) {
		return errors.New("invalid completion assignment address")
	}
	if a.Subject.Kind == SubjectCompletion {
		return errors.New("completion subject carries no issue snapshot row")
	}
	return nil
}

func (a CompletionAssignment) validateSubject() error {
	if a.Subject.Kind == "" {
		if a.Phase != "" {
			return errors.New("unsubjected completion assignment names a phase")
		}
		if a.Issue <= 0 || !project.ValidDecisionID(a.Acceptance) {
			return errors.New("invalid completion assignment scope")
		}
		return nil
	}
	if err := a.Subject.Validate(); err != nil {
		return err
	}
	if !project.ValidExecutionPhase(a.Phase) || !SubjectKindAllowed(a.Subject.Kind, a.Phase) ||
		!ValidPhaseRole(a.Phase, a.Role) {
		return errors.New("completion assignment role does not match its execution phase")
	}
	switch a.Subject.Kind {
	case SubjectIssue:
		if a.Issue <= 0 || !project.ValidDecisionID(a.Acceptance) {
			return errors.New("invalid completion assignment scope")
		}
	case SubjectIntent:
		if a.Issue != 0 || a.Acceptance != "" {
			return errors.New("intent completion assignment carries issue authority")
		}
	default:
		return errors.New("invalid completion assignment subject")
	}
	return nil
}

func (a CompletionAssignment) validateStage() error {
	switch a.Stage {
	case AssignmentAssigned:
		if a.Outcome != "" || a.Reason != "" {
			return errors.New("assigned completion work carries no outcome")
		}
	case AssignmentFinished:
		if !validOutcome(a.Outcome) || a.Outcome == "" || !validAssignReason(a.Reason) {
			return errors.New("finished completion assignment lacks its outcome")
		}
	default:
		return errors.New("invalid completion assignment stage")
	}
	return nil
}

func (a CompletionAssignment) validateHistory() error {
	if !ValidCommit(a.SourceCommit) || !ValidPreparationRef(a.Preparation) || a.Revision < 0 {
		return errors.New("invalid completion assignment bindings")
	}
	if len(a.RunHistory) == 0 || len(a.RunHistory) > MaxDispatchAttempts {
		return errors.New("invalid completion assignment run history")
	}
	seen := make(map[string]bool, len(a.RunHistory))
	for _, run := range a.RunHistory {
		if err := run.Validate(); err != nil {
			return err
		}
		if seen[run.ID] {
			return errors.New("duplicate completion assignment run")
		}
		seen[run.ID] = true
	}
	if a.Reservation != nil {
		if err := a.Reservation.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func (a CompletionAssignment) Validate() error {
	if err := a.validateAddress(); err != nil {
		return err
	}
	if err := a.validateSubject(); err != nil {
		return err
	}
	if err := a.validateStage(); err != nil {
		return err
	}
	return a.validateHistory()
}

// CompletionPublication projects one durable publication: the immutable
// assignment/root/run/acceptance/issue/PR/candidate/base identity plus
// stage, outcome and reason. Decode+validate the canonical record and the
// exact ownership/run joins at capture; preserve the recorded
// provenance. No raw operation/authorization payloads, review work,
// journals, observation stamps or tuples enter.
type CompletionPublication struct {
	ID           string  `json:"id"`
	AssignmentID string  `json:"assignment_id"`
	ProjectID    string  `json:"project_id"`
	Role         string  `json:"role"`
	Acceptance   string  `json:"acceptance"`
	Run          string  `json:"run"`
	Candidate    string  `json:"candidate"`
	BaseSHA      string  `json:"base_sha"`
	TargetBranch string  `json:"target_branch"`
	Stage        string  `json:"stage"`
	Outcome      Outcome `json:"outcome,omitempty"`
	Reason       string  `json:"reason,omitempty"`
	Repository   int64   `json:"repository,string"`
	Issue        int64   `json:"issue,string"`
	Revision     int64   `json:"revision"`
	PRNumber     int64   `json:"pr_number"`
	PRID         int64   `json:"pr_id"`
}

func (p CompletionPublication) validateIdentity() error {
	if !ValidID(p.ID) || !ValidID(p.AssignmentID) || !ValidID(p.Run) {
		return errors.New("invalid completion publication identity")
	}
	if !ValidProjectID(p.ProjectID) || p.Role != project.RoleCoder {
		return errors.New("invalid completion publication address")
	}
	if p.Repository <= 0 || p.Issue <= 0 || p.Revision < 0 {
		return errors.New("invalid completion publication scope")
	}
	if !project.ValidDecisionID(p.Acceptance) {
		return errors.New("invalid completion publication acceptance")
	}
	if !ValidCommit(p.Candidate) || !ValidCommit(p.BaseSHA) || p.Candidate == p.BaseSHA {
		return errors.New("completion publication candidate is not a fresh exact commit")
	}
	if !ValidTargetBranch(p.TargetBranch) {
		return errors.New("invalid completion publication target branch")
	}
	return nil
}

func (p CompletionPublication) validateOpenStage() error {
	if p.Outcome != "" || p.Reason != "" {
		return errors.New("open completion publication carries no outcome")
	}
	if p.PRNumber != 0 || p.PRID != 0 {
		return errors.New("unlinked completion publication names a PR")
	}
	return nil
}

func (p CompletionPublication) validatePublishedStage() error {
	if p.Outcome != Succeeded || p.Reason != PublishReasonLinked {
		return errors.New("published completion publication lacks its outcome")
	}
	if p.PRNumber <= 0 || p.PRID <= 0 {
		return errors.New("published completion publication lacks its PR links")
	}
	return nil
}

func (p CompletionPublication) validateFinishedStage() error {
	if !validPublishReason(p.Reason) || p.Reason == PublishReasonLinked {
		return errors.New("finished completion publication lacks its outcome")
	}
	switch p.Stage {
	case PublicationFailed:
		if p.Outcome != Failed {
			return errors.New("failed completion publication lacks its outcome")
		}
	case PublicationWithdrawn:
		if p.Outcome != Cancelled {
			return errors.New("withdrawn completion publication lacks its outcome")
		}
	case PublicationFenced:
		if p.Outcome != NeedsHuman {
			return errors.New("fenced completion publication lacks its outcome")
		}
	}
	return nil
}

func (p CompletionPublication) Validate() error {
	if err := p.validateIdentity(); err != nil {
		return err
	}
	switch p.Stage {
	case PublicationOpen:
		return p.validateOpenStage()
	case PublicationPublished:
		return p.validatePublishedStage()
	case PublicationFailed, PublicationWithdrawn, PublicationFenced:
		return p.validateFinishedStage()
	default:
		return errors.New("invalid completion publication stage")
	}
}

// CompletionMerge projects one durable merge: the immutable
// publication/assignment identity, acceptance, issue, PR, candidate/base
// refs and commits, merged commit, original closing occurrence, stage,
// outcome and reason. Never invent a verified boolean from a closed
// issue. No raw operation/authorization payloads, history generations or
// observation stamps enter.
type CompletionMerge struct {
	Method                    string  `json:"merge_method,omitempty"`
	OriginalClosingOccurrence string  `json:"original_closing_occurrence,omitempty"`
	MergedCommit              string  `json:"merged_commit,omitempty"`
	ID                        string  `json:"id"`
	PublicationID             string  `json:"publication_id"`
	AssignmentID              string  `json:"assignment_id"`
	ProjectID                 string  `json:"project_id"`
	Role                      string  `json:"role"`
	Acceptance                string  `json:"acceptance"`
	HeadRef                   string  `json:"head_ref"`
	BaseRef                   string  `json:"base_ref"`
	HeadOID                   string  `json:"head_oid"`
	BaseOID                   string  `json:"base_oid"`
	Stage                     string  `json:"stage"`
	Outcome                   Outcome `json:"outcome,omitempty"`
	Reason                    string  `json:"reason,omitempty"`
	Repository                int64   `json:"repository,string"`
	Issue                     int64   `json:"issue,string"`
	PRNumber                  int64   `json:"pr_number"`
	PRID                      int64   `json:"pr_id"`
	IssueID                   int64   `json:"issue_id"`
	Revision                  int64   `json:"revision"`
}

func (m CompletionMerge) validateIdentity() error {
	if !ValidID(m.ID) || !ValidID(m.PublicationID) || !ValidID(m.AssignmentID) {
		return errors.New("invalid completion merge identity")
	}
	if !ValidProjectID(m.ProjectID) || m.Role != project.RoleCoder {
		return errors.New("invalid completion merge address")
	}
	if m.Repository <= 0 || m.Issue <= 0 || m.PRNumber <= 0 || m.PRID <= 0 ||
		m.IssueID <= 0 || m.Revision < 0 {
		return errors.New("invalid completion merge scope")
	}
	if !project.ValidDecisionID(m.Acceptance) {
		return errors.New("invalid completion merge acceptance")
	}
	return nil
}

func (m CompletionMerge) validateRefs() error {
	if !ValidTargetBranch(m.HeadRef) || !ValidTargetBranch(m.BaseRef) || m.HeadRef == m.BaseRef {
		return errors.New("invalid completion merge refs")
	}
	if !ValidCommit(m.HeadOID) || !ValidCommit(m.BaseOID) || m.HeadOID == m.BaseOID {
		return errors.New("completion merge candidate is not a fresh exact commit")
	}
	intent := MergeIntent{Method: m.Method, HeadOID: m.HeadOID}
	if !ValidAutomaticMergeMethod(intent.MergeMethod()) || m.MergedCommit != "" && !intent.AcceptsResult(m.MergedCommit) {
		return errors.New("completion merge realized commit differs from its method")
	}
	if m.OriginalClosingOccurrence != "" && !ValidNativeOccurrence(m.OriginalClosingOccurrence) {
		return errors.New("invalid completion merge closing occurrence")
	}
	return nil
}

func (m CompletionMerge) validateStage() error {
	if !ValidMergeStage(m.Stage) {
		return errors.New("invalid completion merge stage")
	}
	switch m.Stage {
	case MergeOpen:
		if m.Outcome != "" || m.Reason != "" || m.MergedCommit != "" || m.OriginalClosingOccurrence != "" {
			return errors.New("open completion merge carries no outcome")
		}
	default:
		if !validOutcome(m.Outcome) || m.Outcome == "" || !validMergeReason(m.Reason) {
			return errors.New("finished completion merge lacks its outcome")
		}
		if m.Stage == MergeMerged && (m.Outcome != Succeeded || m.Reason != MergeReasonMerged || m.MergedCommit == "") {
			return errors.New("merged completion merge lacks its realized commit")
		}
	}
	return nil
}

func (m CompletionMerge) Validate() error {
	if err := m.validateIdentity(); err != nil {
		return err
	}
	if err := m.validateRefs(); err != nil {
		return err
	}
	return m.validateStage()
}

// CompletionCheck projects one recorded check verdict: native PR/issue
// IDs, head/base, policy revision, record revision, required-check set
// with its digest, verdict, reason and per-check context/state/passed.
// Source assessments validate before observation timestamps and native
// global refresh metadata are omitted. No URLs or logs enter.
type CompletionCheck struct {
	Results        []CheckResult `json:"results"`
	Checks         []string      `json:"checks"`
	HeadRef        string        `json:"head_ref"`
	BaseRef        string        `json:"base_ref"`
	HeadOID        string        `json:"head_oid"`
	BaseOID        string        `json:"base_oid"`
	ChecksDigest   string        `json:"checks_digest"`
	Verdict        string        `json:"verdict"`
	Reason         string        `json:"reason"`
	Repository     int64         `json:"repository,string"`
	PRNumber       int64         `json:"pr_number"`
	PRID           int64         `json:"pr_id"`
	IssueID        int64         `json:"issue_id"`
	PolicyRevision int64         `json:"policy_revision"`
	Revision       int64         `json:"revision"`
}

// NewCompletionCheck projects one validated assessment with observation
// metadata omitted. The source assessment validates first so a malformed
// row fails closed instead of projecting a partial verdict.
func NewCompletionCheck(assessment CheckAssessment) (CompletionCheck, error) {
	if err := assessment.Validate(); err != nil {
		return CompletionCheck{}, err
	}
	check := CompletionCheck{
		Results: append([]CheckResult(nil), assessment.Results...),
		Checks:  append([]string(nil), assessment.Checks...),
		HeadRef: assessment.HeadRef, BaseRef: assessment.BaseRef,
		HeadOID: assessment.HeadOID, BaseOID: assessment.BaseOID,
		ChecksDigest: assessment.ChecksDigest, Verdict: assessment.Verdict,
		Reason: assessment.Reason, Repository: assessment.Repository,
		PRNumber: assessment.PRNumber, PRID: assessment.PRID,
		IssueID: assessment.IssueID, PolicyRevision: assessment.PolicyRevision,
		Revision: assessment.Revision,
	}
	if err := check.Validate(); err != nil {
		return CompletionCheck{}, err
	}
	return check, nil
}

func (c CompletionCheck) validateScope() error {
	if c.Repository <= 0 || c.PRNumber <= 0 || c.PRID <= 0 || c.IssueID <= 0 {
		return errors.New("invalid completion check scope")
	}
	if !ValidTargetBranch(c.HeadRef) || !ValidTargetBranch(c.BaseRef) || c.HeadRef == c.BaseRef {
		return errors.New("invalid completion check refs")
	}
	if !ValidCommit(c.HeadOID) || !ValidCommit(c.BaseOID) {
		return errors.New("invalid completion check commits")
	}
	if c.PolicyRevision < 0 || c.Revision < 0 {
		return errors.New("invalid completion check revision")
	}
	return nil
}

func (c CompletionCheck) validateCheckSet() error {
	if len(c.Checks) == 0 || len(c.Checks) > MaxRequiredChecks {
		return errors.New("completion check requires its nonempty check set")
	}
	seen := make(map[string]bool, len(c.Checks))
	for _, check := range c.Checks {
		if check == "" || len(check) > MaxCheckName || seen[check] {
			return errors.New("invalid completion check name")
		}
		seen[check] = true
	}
	if !ValidDigest(c.ChecksDigest) || c.ChecksDigest != ChecksDigest(c.Checks) {
		return errors.New("completion check digest differs from its set")
	}
	return nil
}

func (c CompletionCheck) validateResults() error {
	if !ValidCheckVerdict(c.Verdict) || c.Reason == "" || len(c.Reason) > MaxCheckName ||
		strings.IndexFunc(c.Reason, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
		return errors.New("invalid completion check verdict")
	}
	if c.Results == nil || len(c.Results) != len(c.Checks) {
		return errors.New("completion check results differ from their set")
	}
	for i, result := range c.Results {
		if err := result.Validate(); err != nil {
			return err
		}
		if result.Context != c.Checks[i] {
			return errors.New("completion check results differ from their set")
		}
	}
	return nil
}

func (c CompletionCheck) Validate() error {
	if err := c.validateScope(); err != nil {
		return err
	}
	if err := c.validateCheckSet(); err != nil {
		return err
	}
	return c.validateResults()
}

// CompletionAuthority projects the standing authority behind the seal:
// the actual effective verdict with its missing reasons and bound
// revisions, the current-head context (project, admitted requirement
// and approval head digests with the verified flag, and the policy
// target branch), the permitted role names, and the admitted
// reassessment minute allowance. No secret grant or config documents,
// no SourceInputs map and no raw setup/config/secret/private bodies
// enter. A false ApprovalVerified is representable and MUST NOT be
// relabeled verified. This grants no new authority: the coarse
// preparation-ready predicate behind EvaluateAuthority never proves a
// head-matching prepared checkout.
type CompletionAuthority struct {
	Effective               EffectiveAuthority `json:"effective"`
	ProjectID               string             `json:"project_id"`
	RequirementSourceCommit string             `json:"requirement_source_commit"`
	RequirementSetupDigest  string             `json:"requirement_setup_digest"`
	RequirementInputsDigest string             `json:"requirement_inputs_digest"`
	ApprovalEffectsDigest   string             `json:"approval_effects_digest"`
	ApprovalReadinessDigest string             `json:"approval_readiness_digest"`
	ApprovalVerified        bool               `json:"approval_verified"`
	TargetBranch            string             `json:"target_branch"`
	Roles                   []string           `json:"roles"`
	ReassessmentMinutes     int                `json:"reassessment_minutes"`
}

// PermittedCompletionRoles projects the sorted unique permitted roles:
// each configured role's explicitly selected active sponsorship.
// A missing or withdrawn sponsorship permits nothing: the
// result is explicit empty, never the configured set. An explicitly
// empty set is representable and confers no authority.
func PermittedCompletionRoles(configured map[string]RoleSelection, sponsorships []Sponsorship) []string {
	permitted := []string{}
	for role, selection := range configured {
		if !project.ValidFactoryRole(role) || selection.Account == "" {
			continue
		}
		for _, sponsorship := range sponsorships {
			if sponsorship.Connection != selection.Account || !sponsorship.Active {
				continue
			}
			for _, allowed := range sponsorship.Roles {
				if allowed == role {
					permitted = append(permitted, role)
					break
				}
			}
			break
		}
	}
	sort.Strings(permitted)
	return permitted
}

func (a CompletionAuthority) validateVerdict() error {
	if a.Effective.Missing == nil {
		return errors.New("completion authority lacks its verdict")
	}
	if a.Effective.Authority.Policy < 0 || a.Effective.Authority.Operator < 0 ||
		a.Effective.Authority.Capacity < 0 || a.Effective.Authority.Sponsorship < 0 ||
		a.Effective.Authority.Environment < 0 {
		return errors.New("invalid completion authority revisions")
	}
	if !project.ValidDecisionID(a.Effective.Authority.RequirementsID) ||
		!project.ValidDecisionID(a.Effective.Authority.ApprovalID) {
		return errors.New("completion authority lacks its current head identities")
	}
	return nil
}

func (a CompletionAuthority) validateScope() error {
	if !project.ValidID(a.ProjectID) {
		return errors.New("invalid completion authority project")
	}
	if !ValidCommit(a.RequirementSourceCommit) {
		return errors.New("invalid completion authority requirement source")
	}
	if !ValidDigest(a.RequirementSetupDigest) || !ValidDigest(a.RequirementInputsDigest) ||
		!ValidDigest(a.ApprovalEffectsDigest) || !ValidDigest(a.ApprovalReadinessDigest) {
		return errors.New("invalid completion authority head digests")
	}
	if !ValidTargetBranch(a.TargetBranch) {
		return errors.New("invalid completion authority target")
	}
	return nil
}

func (a CompletionAuthority) validateRoles() error {
	if a.Roles == nil {
		return errors.New("completion authority lacks its permitted roles")
	}
	prior := ""
	for _, role := range a.Roles {
		if !project.ValidFactoryRole(role) || role <= prior {
			return errors.New("completion authority roles are not sorted unique")
		}
		prior = role
	}
	if a.ReassessmentMinutes < MinReassessmentMinutes || a.ReassessmentMinutes > MaxReassessmentMinutes {
		return errors.New("completion authority minutes outside their bound")
	}
	return nil
}

func (a CompletionAuthority) Validate() error {
	if err := a.validateVerdict(); err != nil {
		return err
	}
	if err := a.validateScope(); err != nil {
		return err
	}
	return a.validateRoles()
}

// CompletionMaterial is the canonical model-safe local context: the
// repository plus complete non-nil ordered sections and authority.
// Field order is fixed so the canonical encoding is digest-stable.
type CompletionMaterial struct {
	Intents      []CompletionIntentScope `json:"intents"`
	Acceptances  []Acceptance            `json:"acceptances"`
	Withdrawals  []CompletionWithdrawal  `json:"withdrawals"`
	Controls     []IssueControl          `json:"controls"`
	Preparations []CompletionPreparation `json:"preparations"`
	Plans        []InvestigationPlan     `json:"plans"`
	PlanHeads    []CompletionPlanHead    `json:"plan_heads"`
	PlanTasks    []CompletionPlanTask    `json:"plan_tasks"`
	Assignments  []CompletionAssignment  `json:"assignments"`
	Publications []CompletionPublication `json:"publications"`
	Merges       []CompletionMerge       `json:"merges"`
	Checks       []CompletionCheck       `json:"checks"`
	Authority    CompletionAuthority     `json:"authority"`
	Repository   int64                   `json:"repository,string"`
}

func compareIssueIndex(a, b string) int {
	if len(a) != len(b) {
		if len(a) < len(b) {
			return -1
		}
		return 1
	}
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// Validate checks scope, complete sections, IDs, cross-references,
// deterministic order and the full encoded bound. A nil section is
// missing data and refuses: query layers must emit explicit empty
// arrays instead.
type completionMaterialIndex struct {
	acceptances  map[string]Acceptance
	plans        map[string]map[string]bool
	planTasks    map[string]map[string]map[string]bool
	assignments  map[string]CompletionAssignment
	publications map[string]bool
}

func (m CompletionMaterial) validateSections() error {
	if m.Repository <= 0 {
		return errors.New("invalid completion material repository")
	}
	sections := []struct {
		name  string
		count int
		empty bool
	}{
		{"intents", len(m.Intents), m.Intents == nil},
		{"acceptances", len(m.Acceptances), m.Acceptances == nil},
		{"withdrawals", len(m.Withdrawals), m.Withdrawals == nil},
		{"controls", len(m.Controls), m.Controls == nil},
		{"preparations", len(m.Preparations), m.Preparations == nil},
		{"plans", len(m.Plans), m.Plans == nil},
		{"plan heads", len(m.PlanHeads), m.PlanHeads == nil},
		{"plan tasks", len(m.PlanTasks), m.PlanTasks == nil},
		{"assignments", len(m.Assignments), m.Assignments == nil},
		{"publications", len(m.Publications), m.Publications == nil},
		{"merges", len(m.Merges), m.Merges == nil},
		{"checks", len(m.Checks), m.Checks == nil},
	}
	for _, section := range sections {
		if section.empty {
			return errors.New("completion material lacks its " + section.name)
		}
		if section.count > MaxReassessmentIssues {
			return errors.New("completion material " + section.name + " exceed their bound")
		}
	}
	return nil
}

func (m CompletionMaterial) validateAcceptances(idx *completionMaterialIndex) error {
	acceptances := make(map[string]Acceptance, len(m.Acceptances))
	priorIssue, priorAcceptance := "", ""
	for _, acceptance := range m.Acceptances {
		if err := acceptance.Validate(); err != nil {
			return err
		}
		if acceptance.Repository != m.Repository {
			return errors.New("completion material acceptance names another repository")
		}
		if priorAcceptance != "" {
			order := compareIssueIndex(acceptance.IssueIndex, priorIssue)
			if order < 0 || (order == 0 && acceptance.ID <= priorAcceptance) {
				return errors.New("completion material acceptances are not ordered")
			}
		}
		priorIssue, priorAcceptance = acceptance.IssueIndex, acceptance.ID
		if _, dup := acceptances[acceptance.ID]; dup {
			return errors.New("duplicate completion material acceptance")
		}
		acceptances[acceptance.ID] = acceptance
	}
	for i, withdrawal := range m.Withdrawals {
		if err := withdrawal.Validate(); err != nil {
			return err
		}
		if i > 0 {
			prev := m.Withdrawals[i-1]
			if withdrawal.Issue < prev.Issue ||
				(withdrawal.Issue == prev.Issue && withdrawal.AcceptanceID <= prev.AcceptanceID) {
				return errors.New("completion material withdrawals are not ordered")
			}
		}
		if _, ok := acceptances[withdrawal.AcceptanceID]; !ok {
			return errors.New("completion material withdrawal lacks its acceptance body")
		}
	}
	idx.acceptances = acceptances
	return nil
}

func (m CompletionMaterial) validateControls(idx *completionMaterialIndex) error {
	priorControl := int64(0)
	for i, control := range m.Controls {
		if err := control.Validate(); err != nil {
			return err
		}
		if control.Repository != m.Repository {
			return errors.New("completion material control names another repository")
		}
		if i > 0 && control.Issue <= priorControl {
			return errors.New("completion material controls are not ordered")
		}
		priorControl = control.Issue
		if control.Acceptance != "" {
			if _, ok := idx.acceptances[control.Acceptance]; !ok {
				return errors.New("completion material control lacks its acceptance body")
			}
		}
	}
	priorPreparation := ""
	for _, preparation := range m.Preparations {
		if err := preparation.Validate(); err != nil {
			return err
		}
		if preparation.ID <= priorPreparation {
			return errors.New("completion material preparations are not ordered")
		}
		priorPreparation = preparation.ID
	}
	return nil
}

func (m CompletionMaterial) indexPlans(idx *completionMaterialIndex) error {
	plans := make(map[string]map[string]bool, len(m.Plans))
	planTasks := make(map[string]map[string]map[string]bool, len(m.Plans))
	priorRoot, priorPlan := "", ""
	for _, plan := range m.Plans {
		if err := plan.Validate(); err != nil {
			return err
		}
		if priorRoot != "" || priorPlan != "" {
			if plan.AttemptRoot < priorRoot ||
				(plan.AttemptRoot == priorRoot && plan.ID <= priorPlan) {
				return errors.New("completion material plans are not ordered")
			}
		}
		priorRoot, priorPlan = plan.AttemptRoot, plan.ID
		revision, ok := plans[plan.AttemptRoot]
		if !ok {
			revision = make(map[string]bool)
			plans[plan.AttemptRoot] = revision
		}
		if revision[plan.ID] {
			return errors.New("duplicate completion material plan")
		}
		revision[plan.ID] = true
		tasks, ok := planTasks[plan.AttemptRoot]
		if !ok {
			tasks = make(map[string]map[string]bool)
			planTasks[plan.AttemptRoot] = tasks
		}
		known := make(map[string]bool, len(plan.Tasks))
		for _, task := range plan.Tasks {
			known[task.ID] = true
		}
		tasks[plan.ID] = known
	}
	idx.plans = plans
	idx.planTasks = planTasks
	return nil
}

func (m CompletionMaterial) validatePlanHeads(idx *completionMaterialIndex) error {
	priorHead := ""
	for _, head := range m.PlanHeads {
		if err := head.Validate(); err != nil {
			return err
		}
		if head.AttemptRoot <= priorHead && priorHead != "" {
			return errors.New("completion material plan heads are not ordered")
		}
		priorHead = head.AttemptRoot
		if !idx.plans[head.AttemptRoot][head.PlanID] {
			return errors.New("completion material plan head lacks its plan revision")
		}
	}
	seenHeads := make(map[string]bool, len(m.PlanHeads))
	for _, head := range m.PlanHeads {
		if seenHeads[head.AttemptRoot] {
			return errors.New("duplicate completion material plan head")
		}
		seenHeads[head.AttemptRoot] = true
	}
	return nil
}

func (m CompletionMaterial) validatePlanTasks(idx *completionMaterialIndex) error {
	priorTaskRoot, priorTaskPlan, priorTask := "", "", ""
	for _, task := range m.PlanTasks {
		if err := task.Validate(); err != nil {
			return err
		}
		if priorTaskRoot != "" || priorTaskPlan != "" || priorTask != "" {
			if task.AttemptRoot < priorTaskRoot ||
				(task.AttemptRoot == priorTaskRoot && task.PlanID < priorTaskPlan) ||
				(task.AttemptRoot == priorTaskRoot && task.PlanID == priorTaskPlan && task.TaskID <= priorTask) {
				return errors.New("completion material plan tasks are not ordered")
			}
		}
		priorTaskRoot, priorTaskPlan, priorTask = task.AttemptRoot, task.PlanID, task.TaskID
		known, ok := idx.planTasks[task.AttemptRoot][task.PlanID]
		if !ok {
			return errors.New("completion material plan task lacks its plan revision")
		}
		if !known[task.TaskID] {
			return errors.New("completion material plan task is not a plan task")
		}
	}
	return nil
}

func (m CompletionMaterial) validatePlanRefs(idx *completionMaterialIndex) error {
	if err := m.validatePlanHeads(idx); err != nil {
		return err
	}
	return m.validatePlanTasks(idx)
}

func (m CompletionMaterial) validatePlans(idx *completionMaterialIndex) error {
	if err := m.indexPlans(idx); err != nil {
		return err
	}
	return m.validatePlanRefs(idx)
}

func (m CompletionMaterial) validateAssignments(idx *completionMaterialIndex) error {
	assignments := make(map[string]CompletionAssignment, len(m.Assignments))
	priorAssignment := ""
	for _, assignment := range m.Assignments {
		if err := assignment.Validate(); err != nil {
			return err
		}
		if assignment.ID <= priorAssignment && priorAssignment != "" {
			return errors.New("completion material assignments are not ordered")
		}
		priorAssignment = assignment.ID
		if _, dup := assignments[assignment.ID]; dup {
			return errors.New("duplicate completion material assignment")
		}
		assignments[assignment.ID] = assignment
	}
	for _, assignment := range m.Assignments {
		if _, ok := assignments[assignment.AttemptRoot]; !ok {
			return errors.New("completion material assignment lacks its attempt root")
		}
	}
	idx.assignments = assignments
	return nil
}

func (m CompletionMaterial) validatePublications(idx *completionMaterialIndex) error {
	publications := make(map[string]bool, len(m.Publications))
	priorPublication := ""
	for _, publication := range m.Publications {
		if err := publication.Validate(); err != nil {
			return err
		}
		if publication.Repository != m.Repository {
			return errors.New("completion material publication names another repository")
		}
		if publication.AssignmentID <= priorPublication && priorPublication != "" {
			return errors.New("completion material publications are not ordered")
		}
		priorPublication = publication.AssignmentID
		if publications[publication.ID] {
			return errors.New("duplicate completion material publication")
		}
		publications[publication.ID] = true
		if _, ok := idx.assignments[publication.AssignmentID]; !ok {
			return errors.New("completion material publication lacks its assignment")
		}
		if _, ok := idx.acceptances[publication.Acceptance]; !ok {
			return errors.New("completion material publication lacks its acceptance body")
		}
	}
	priorMerge := ""
	seenMerges := make(map[string]bool, len(m.Merges))
	for _, merge := range m.Merges {
		if err := merge.Validate(); err != nil {
			return err
		}
		if merge.Repository != m.Repository {
			return errors.New("completion material merge names another repository")
		}
		if merge.PublicationID <= priorMerge && priorMerge != "" {
			return errors.New("completion material merges are not ordered")
		}
		priorMerge = merge.PublicationID
		if seenMerges[merge.PublicationID] {
			return errors.New("duplicate completion material merge")
		}
		seenMerges[merge.PublicationID] = true
		if !publications[merge.PublicationID] {
			return errors.New("completion material merge lacks its publication")
		}
		if _, ok := idx.assignments[merge.AssignmentID]; !ok {
			return errors.New("completion material merge lacks its assignment")
		}
		if _, ok := idx.acceptances[merge.Acceptance]; !ok {
			return errors.New("completion material merge lacks its acceptance body")
		}
	}
	idx.publications = publications
	return nil
}

func (m CompletionMaterial) validateChecksIntents(idx *completionMaterialIndex) error {
	priorPR := int64(0)
	for i, check := range m.Checks {
		if err := check.Validate(); err != nil {
			return err
		}
		if check.Repository != m.Repository {
			return errors.New("completion material check names another repository")
		}
		if i > 0 && check.PRNumber <= priorPR {
			return errors.New("completion material checks are not ordered")
		}
		priorPR = check.PRNumber
	}
	priorIntent, priorRevision := "", ""
	for _, scope := range m.Intents {
		if err := scope.Validate(); err != nil {
			return err
		}
		if scope.Repository != m.Repository {
			return errors.New("completion material scope names another repository")
		}
		if priorIntent != "" || priorRevision != "" {
			if scope.IntentID < priorIntent ||
				(scope.IntentID == priorIntent && scope.RevisionID <= priorRevision) {
				return errors.New("completion material intents are not ordered")
			}
		}
		priorIntent, priorRevision = scope.IntentID, scope.RevisionID
		for _, binding := range scope.Bindings {
			if _, ok := idx.acceptances[binding.AcceptanceID]; !ok {
				return errors.New("completion material binding lacks its acceptance body")
			}
		}
	}
	for _, plan := range m.Plans {
		if _, ok := idx.assignments[plan.AttemptRoot]; !ok {
			if !isCompletionMaterialRoot(plan.AttemptRoot, idx.assignments) {
				return errors.New("completion material plan lacks its assignment root")
			}
		}
	}
	return nil
}

func (m CompletionMaterial) validateSeal() error {
	if err := m.Authority.Validate(); err != nil {
		return err
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if len(raw) > MaxCompletionMaterialBytes {
		return errors.New("encoded completion material exceeds its bound")
	}
	return nil
}

func (m CompletionMaterial) Validate() error {
	if err := m.validateSections(); err != nil {
		return err
	}
	idx := &completionMaterialIndex{}
	if err := m.validateAcceptances(idx); err != nil {
		return err
	}
	if err := m.validateControls(idx); err != nil {
		return err
	}
	if err := m.validatePlans(idx); err != nil {
		return err
	}
	if err := m.validateAssignments(idx); err != nil {
		return err
	}
	if err := m.validatePublications(idx); err != nil {
		return err
	}
	if err := m.validateChecksIntents(idx); err != nil {
		return err
	}
	return m.validateSeal()
}

// isCompletionMaterialRoot reports whether root is an attempt root carried
// by the assignment section (a root assignment carries its own ID).
func isCompletionMaterialRoot(root string, assignments map[string]CompletionAssignment) bool {
	for _, assignment := range assignments {
		if assignment.AttemptRoot == root {
			return true
		}
	}
	return false
}

// canonicalJSON encodes the validated complete material in its
// digest-stable canonical form: the exact bytes sealed digests fold.
func (m CompletionMaterial) canonicalJSON() ([]byte, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(m)
}

// Digest hashes canonical JSON of the validated complete material.
// CompletionLocal.ScopeDigest becomes exactly this digest; no second
// competing scope hash exists.
func (m CompletionMaterial) Digest() (string, error) {
	raw, err := m.canonicalJSON()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
