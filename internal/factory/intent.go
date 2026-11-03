package factory

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/levitateos/sodaos/internal/project"
)

// Department is private organizational context owned by its creator. It grants
// no repository, Project, account or spending permission.
type Department struct {
	ID          string `json:"id"`
	Owner       int64  `json:"owner,string"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

func (d Department) Validate() error {
	if !project.ValidDecisionID(d.ID) || d.Owner <= 0 || !intentText(d.Name, 120) || !intentText(d.Description, 2000) {
		return errors.New("invalid department")
	}
	return nil
}

// ProductIdea describes a requested fact, supported/autonomous choice or
// assumption with its consequence and attributable decision basis.
// Source distinguishes requested facts from evidence-supported choices,
// reversible autonomous choices and assumptions. Provenance distinguishes
// explicit human decisions from autonomous ones; human provenance must bind
// Actor to the revision owner. Material marks consequential assumptions that
// block dependent native work while unresolved. Empty Source/Provenance is
// explicitly unclassified unfinished draft input and infers no authorship;
// ideas admitted into effective accepted work must state their basis.
type ProductIdea struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Consequence string `json:"consequence"`
	Assumption  bool   `json:"assumption"`
	Decision    string `json:"decision"`
	Correction  string `json:"correction,omitempty"`
	Source      string `json:"source,omitempty"`
	Provenance  string `json:"provenance,omitempty"`
	Actor       int64  `json:"actor,string,omitempty"`
	Evidence    string `json:"evidence,omitempty"`
	Material    bool   `json:"material,omitempty"`
}

type IntentTask struct {
	Key       string   `json:"key"`
	Objective string   `json:"objective"`
	Outcomes  []string `json:"outcomes"`
	IdeaIDs   []string `json:"idea_ids"`
	DependsOn []string `json:"depends_on,omitempty"`
}

// IntentRevision is immutable review input or accepted product authority. An
// acceptance covers this exact product direction and bounded derived graph;
// execution still requires current independent grants and native evidence.
// EffectiveOutcomes names the explicit authorized idea subset for native work.
// Unrelated authorized outcomes can progress while an unresolved consequential
// branch stays pending; tasks must only reference effective authorized ideas.
// Rejected and abandoned are terminal private draft states preserving history.
type IntentRevision struct {
	ID                string        `json:"id"`
	Intent            string        `json:"intent"`
	Predecessor       string        `json:"predecessor,omitempty"`
	Owner             int64         `json:"owner,string"`
	Repository        int64         `json:"repository,string"`
	Department        string        `json:"department,omitempty"`
	Objective         string        `json:"objective"`
	Approach          string        `json:"approach,omitempty"`
	Ideas             []ProductIdea `json:"ideas"`
	Tasks             []IntentTask  `json:"tasks"`
	State             string        `json:"state"`
	MaxIssues         int           `json:"max_issues"`
	PlanningCalls     int           `json:"planning_calls"`
	CreatedUnix       int64         `json:"created_unix"`
	EffectiveOutcomes []string      `json:"effective_outcomes,omitempty"`
}

func intentText(s string, limit int) bool {
	return strings.TrimSpace(s) != "" && utf8.RuneCountInString(s) <= limit && !strings.ContainsRune(s, 0)
}

func (r IntentRevision) Validate() error {
	if err := checkIntentIdentity(r); err != nil {
		return err
	}
	ideas, err := indexRevisionIdeas(r)
	if err != nil {
		return err
	}
	effective, err := checkEffectiveOutcomes(r, ideas)
	if err != nil {
		return err
	}
	tasks, err := indexRevisionTasks(r, ideas, effective)
	if err != nil {
		return err
	}
	return checkIntentTaskGraph(tasks)
}

func checkIntentIdentity(r IntentRevision) error {
	if !project.ValidDecisionID(r.ID) || !project.ValidDecisionID(r.Intent) || r.Owner <= 0 || r.CreatedUnix <= 0 || !intentText(r.Objective, 16000) {
		return errors.New("invalid intent identity or objective")
	}
	if r.Predecessor != "" && !project.ValidDecisionID(r.Predecessor) || r.Department != "" && !project.ValidDecisionID(r.Department) {
		return errors.New("invalid intent reference")
	}
	if r.Approach != "" && r.Approach != "incremental" && r.Approach != "complete_first" {
		return errors.New("invalid implementation approach")
	}
	if r.State != "draft" && r.State != "accepted" && r.State != "declined" && r.State != "rejected" && r.State != "abandoned" {
		return errors.New("invalid intent state")
	}
	return checkIntentBounds(r)
}

func checkIntentBounds(r IntentRevision) error {
	if r.Repository < 0 || r.MaxIssues < 1 || r.MaxIssues > 64 || r.PlanningCalls < 0 || r.PlanningCalls > 100 || len(r.Ideas) > 50 || len(r.Tasks) > r.MaxIssues || len(r.EffectiveOutcomes) > 50 {
		return errors.New("invalid intent bounds")
	}
	if r.State == "accepted" && (r.Repository == 0 || len(r.Tasks) == 0) {
		return errors.New("accepted intent needs repository and work")
	}
	return nil
}

func indexRevisionIdeas(r IntentRevision) (map[string]ProductIdea, error) {
	ideas := map[string]ProductIdea{}
	for _, idea := range r.Ideas {
		if err := checkProductIdeaShape(idea, ideas); err != nil {
			return nil, err
		}
		if err := checkIdeaAttribution(r, idea); err != nil {
			return nil, err
		}
		if err := checkIdeaBasis(r, idea); err != nil {
			return nil, err
		}
		ideas[idea.ID] = idea
	}
	return ideas, nil
}

func checkProductIdeaShape(idea ProductIdea, ideas map[string]ProductIdea) error {
	if !project.ValidDecisionID(idea.ID) || !intentText(idea.Description, 4000) || !intentText(idea.Consequence, 4000) {
		return errors.New("invalid product idea")
	}
	if _, exists := ideas[idea.ID]; exists {
		return errors.New("duplicate product idea")
	}
	if idea.Decision != "pending" && idea.Decision != "accept" && idea.Decision != "decline" && idea.Decision != "edit" {
		return errors.New("invalid product decision")
	}
	if idea.Decision == "edit" && !intentText(idea.Correction, 4000) {
		return errors.New("edited idea needs correction")
	}
	if idea.Source != "" && idea.Source != "requested" && idea.Source != "supported" && idea.Source != "autonomous" && idea.Source != "assumption" {
		return errors.New("invalid product choice source")
	}
	return checkIdeaProvenance(idea)
}

func checkIdeaProvenance(idea ProductIdea) error {
	if idea.Provenance != "" && idea.Provenance != "human" && idea.Provenance != "autonomous" {
		return errors.New("invalid product choice provenance")
	}
	if idea.Source == "assumption" && !idea.Assumption {
		return errors.New("assumption source needs assumption flag")
	}
	if idea.Assumption && idea.Source != "" && idea.Source != "assumption" {
		return errors.New("assumption needs assumption source")
	}
	return nil
}

func checkIdeaAttribution(r IntentRevision, idea ProductIdea) error {
	if idea.Provenance == "human" {
		if idea.Actor != r.Owner || idea.Actor <= 0 || idea.Decision == "pending" {
			return errors.New("forged human attribution")
		}
	}
	if idea.Provenance == "autonomous" {
		if idea.Actor != 0 {
			return errors.New("autonomous choice cannot carry human actor")
		}
		if idea.Decision == "edit" {
			return errors.New("corrections are explicit human decisions")
		}
	}
	if idea.Actor != 0 && (idea.Provenance != "human" || idea.Actor != r.Owner) {
		return errors.New("forged human attribution")
	}
	return nil
}

func checkIdeaBasis(r IntentRevision, idea ProductIdea) error {
	if idea.Source == "requested" && (idea.Provenance == "autonomous" || idea.Decision == "pending") {
		return errors.New("requested facts are explicit human decisions")
	}
	if idea.Source == "requested" && idea.Provenance == "" && idea.Decision != "accept" && idea.Decision != "edit" && idea.Decision != "decline" {
		return errors.New("requested facts are explicit human decisions")
	}
	if idea.Source == "supported" && (idea.Decision == "accept" || idea.Decision == "edit") && !intentText(idea.Evidence, 4000) {
		return errors.New("supported choices need evidence")
	}
	if idea.Evidence != "" && !intentText(idea.Evidence, 4000) {
		return errors.New("invalid choice evidence")
	}
	if r.State == "accepted" && idea.Decision != "pending" && (idea.Source == "" || idea.Provenance == "") {
		return errors.New("effective work needs an explicit decision basis")
	}
	return nil
}

func checkEffectiveOutcomes(r IntentRevision, ideas map[string]ProductIdea) (map[string]bool, error) {
	effective := map[string]bool{}
	for _, id := range r.EffectiveOutcomes {
		if !project.ValidDecisionID(id) || effective[id] {
			return nil, errors.New("invalid effective outcome")
		}
		idea, exists := ideas[id]
		if !exists || idea.Decision == "decline" || idea.Decision == "pending" {
			return nil, errors.New("effective outcomes must be authorized choices")
		}
		effective[id] = true
	}
	return effective, nil
}

func indexRevisionTasks(r IntentRevision, ideas map[string]ProductIdea, effective map[string]bool) (map[string]IntentTask, error) {
	tasks := map[string]IntentTask{}
	for _, task := range r.Tasks {
		if !project.ValidDecisionID(task.Key) || !intentText(task.Objective, 4000) || len(task.Outcomes) == 0 || len(task.Outcomes) > 20 || len(task.IdeaIDs) == 0 {
			return nil, errors.New("invalid intent task")
		}
		if _, exists := tasks[task.Key]; exists {
			return nil, errors.New("duplicate task")
		}
		for _, outcome := range task.Outcomes {
			if !intentText(outcome, 4000) {
				return nil, errors.New("invalid task outcome")
			}
		}
		for _, id := range task.IdeaIDs {
			idea, exists := ideas[id]
			if !exists {
				return nil, errors.New("task references unknown product choice")
			}
			if idea.Decision == "decline" {
				return nil, errors.New("task references declined outcome")
			}
			if err := checkTaskIdeaBinding(r, idea, id, effective); err != nil {
				return nil, err
			}
		}
		tasks[task.Key] = task
	}
	return tasks, nil
}

func checkTaskIdeaBinding(r IntentRevision, idea ProductIdea, id string, effective map[string]bool) error {
	if r.State != "accepted" {
		return nil
	}
	if idea.Decision == "pending" {
		if idea.Material || idea.Source == "assumption" || idea.Source == "requested" {
			return errors.New("task references unresolved consequential scope")
		}
		return errors.New("task references unresolved scope")
	}
	if len(effective) > 0 && !effective[id] {
		return errors.New("task exceeds effective authorized outcomes")
	}
	if len(effective) == 0 && idea.Decision != "accept" && idea.Decision != "edit" {
		return errors.New("task exceeds accepted product choices")
	}
	return nil
}

func checkIntentTaskGraph(tasks map[string]IntentTask) error {
	visited, active := map[string]bool{}, map[string]bool{}
	for key := range tasks {
		if err := visitIntentTask(tasks, visited, active, key); err != nil {
			return err
		}
	}
	return nil
}

func visitIntentTask(tasks map[string]IntentTask, visited, active map[string]bool, key string) error {
	if active[key] {
		return errors.New("cyclic intent dependencies")
	}
	if visited[key] {
		return nil
	}
	task, exists := tasks[key]
	if !exists {
		return errors.New("unknown intent dependency")
	}
	active[key] = true
	seen := map[string]bool{}
	for _, dep := range task.DependsOn {
		if seen[dep] {
			return errors.New("duplicate intent dependency")
		}
		seen[dep] = true
		if err := visitIntentTask(tasks, visited, active, dep); err != nil {
			return err
		}
	}
	active[key] = false
	visited[key] = true
	return nil
}

func (r IntentRevision) Digest() string {
	raw, _ := json.Marshal(r)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// IntentSummary is a bounded owner-private list/resume entry. It carries
// stable identities and current state without exposing another actor's drafts
// or inaccessible private details. Full revision content stays behind the
// private current-revision read.
type IntentSummary struct {
	Intent         string   `json:"intent"`
	Revision       string   `json:"revision"`
	State          string   `json:"state"`
	Objective      string   `json:"objective"`
	Repository     int64    `json:"repository,string"`
	Department     string   `json:"department,omitempty"`
	CreatedUnix    int64    `json:"created_unix"`
	EffectiveCount int      `json:"effective_count"`
	TaskCount      int      `json:"task_count"`
	Outcomes       []string `json:"effective_outcomes,omitempty"`
}

// Summary derives a list entry from a validated current revision.
func (r IntentRevision) Summary() IntentSummary {
	objective := r.Objective
	if len(objective) > 280 {
		objective = objective[:280]
	}
	resolved := ResolvedScope(r)
	return IntentSummary{
		Intent: r.Intent, Revision: r.ID, State: r.State, Objective: objective,
		Repository: r.Repository, Department: r.Department, CreatedUnix: r.CreatedUnix,
		EffectiveCount: len(resolved), TaskCount: len(r.Tasks), Outcomes: resolved,
	}
}

// IntentListFilter bounds an owner-private request listing. Cursor is the last
// seen intent ID; empty starts from the beginning. Limit is 1..50.
type IntentListFilter struct {
	Cursor     string
	State      string
	Repository int64
	Department string
	Limit      int
}

func (f IntentListFilter) Validate() error {
	if f.Cursor != "" && !project.ValidDecisionID(f.Cursor) {
		return errors.New("invalid list cursor")
	}
	if f.State != "" && f.State != "draft" && f.State != "accepted" && f.State != "declined" && f.State != "rejected" && f.State != "abandoned" {
		return errors.New("invalid list state")
	}
	if f.Repository < 0 || f.Department != "" && !project.ValidDecisionID(f.Department) {
		return errors.New("invalid list context")
	}
	if f.Limit < 1 || f.Limit > 50 {
		return errors.New("invalid list limit")
	}
	return nil
}
