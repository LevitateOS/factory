package factory

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Communications remain reports by their admitted worker. Library changes
// commit through their owners in the same post transaction.
const MCPMaxCommunicationBytes = 8192

var MCPCommunicationTools = []MCPTool{MCPToolAsk, MCPToolAnswer, MCPToolReply, MCPToolFlag, MCPToolLearned, MCPToolSuggest, MCPToolPartial, MCPToolBlocked, MCPToolStuck, MCPToolRecordDecision}

func MCPIsCommunication(tool MCPTool) bool {
	// Typed journey/diagnosis observations have their own bounded decoders and
	// durable recorders, but carry the same report-only authority as these outlets.
	if tool == MCPToolRecordJourneyResult || tool == MCPToolRecordDiagnosis || tool == MCPToolDraftRepair {
		return true
	}
	for _, known := range MCPCommunicationTools {
		if tool == known {
			return true
		}
	}
	return false
}

// FeedCommunication carries content only. Attribution, repository, issue, run,
// assignment and time come from the authenticated capability and stored work.
type FeedCommunication struct {
	IntentSource    *FeedIntentSource  `json:"intent_source,omitempty"`
	PlanSource      *FeedPlanSource    `json:"plan_source,omitempty"`
	Commits         []FeedCommit       `json:"commits,omitempty"`
	Source          *FeedSourceExcerpt `json:"source,omitempty"`
	LibraryItemID   string             `json:"library_item_id,omitempty"`
	LibraryRevision int64              `json:"library_revision,omitempty"`
	Artifacts       []FeedArtifact     `json:"artifacts,omitempty"`
	QuestionState   string             `json:"question_state,omitempty"`
	// Disposition and DispositionReason are server-assigned from the
	// recorded disposition beside the post; worker payloads carrying
	// them refuse like question_state.
	Disposition       string                  `json:"disposition,omitempty"`
	DispositionReason string                  `json:"disposition_reason,omitempty"`
	Kind              string                  `json:"kind"`
	ComponentID       string                  `json:"component_id,omitempty"`
	Title             string                  `json:"title"`
	Summary           string                  `json:"summary"`
	Question          string                  `json:"question,omitempty"`
	Resolution        string                  `json:"resolution,omitempty"`
	Scope             string                  `json:"scope,omitempty"`
	Severity          string                  `json:"severity,omitempty"`
	EvidenceRefs      []string                `json:"evidence_refs,omitempty"`
	Options           []string                `json:"options,omitempty"`
	Completed         []string                `json:"completed,omitempty"`
	Remaining         []string                `json:"remaining,omitempty"`
	Attempts          []string                `json:"attempts,omitempty"`
	Decision          string                  `json:"decision,omitempty"`
	DecisionOwner     string                  `json:"decision_owner,omitempty"`
	Rationale         string                  `json:"rationale,omitempty"`
	Alternatives      []string                `json:"alternatives,omitempty"`
	ParentID          string                  `json:"parent_id,omitempty"`
	RevisesItem       string                  `json:"revises_item,omitempty"`
	ExpectedRevision  int64                   `json:"expected_revision,omitempty"`
	Challenge         bool                    `json:"challenge,omitempty"`
	Items             []FeedCommunicationItem `json:"items,omitempty"`
	Before            string                  `json:"before,omitempty"`
	After             string                  `json:"after,omitempty"`
}

// FeedIntentSource points at the immutable native choice. Only its canonical
// store writer may publish it; a worker cannot claim an accepted human choice.
type FeedIntentSource struct {
	Intent      string `json:"intent"`
	Revision    string `json:"revision"`
	Predecessor string `json:"predecessor,omitempty"`
	State       string `json:"state"`
	Owner       int64  `json:"owner,string"`
}

// FeedPlanSource identifies the immutable native plan revision, separately
// from the current admitted plan head and from execution/checkpoint outcomes.
type FeedPlanSource struct {
	AttemptRoot string `json:"attempt_root"`
	Plan        string `json:"plan"`
	Digest      string `json:"digest"`
	Source      string `json:"source_commit"`
	Generation  int    `json:"generation"`
}

type FeedCommit struct {
	Revision string `json:"revision"`
	Summary  string `json:"summary"`
}

// Source excerpts are attributed worker evidence, never proof of a passed check.
type FeedSourceExcerpt struct {
	Path           string `json:"path"`
	Revision       string `json:"revision,omitempty"`
	StartLine      int    `json:"start_line"`
	Code           string `json:"code"`
	After          string `json:"after,omitempty"`
	HighlightLines []int  `json:"highlight_lines,omitempty"`
}

type FeedCommunicationItem struct {
	ID        string   `json:"id,omitempty"`
	DependsOn []string `json:"depends_on,omitempty"`
	Label     string   `json:"label"`
	Detail    string   `json:"detail,omitempty"`
	State     string   `json:"state,omitempty"`
}

// DecodeMCPCommunication rejects identity fields and unknown content fields.
// Kind is derived from the tool, never supplied by the worker.
func DecodeMCPCommunication(tool MCPTool, payload []byte) (FeedCommunication, error) {
	var out FeedCommunication
	if !MCPIsCommunication(tool) {
		return out, errors.New("not a communication outlet")
	}
	if len(payload) > MCPMaxCommunicationBytes {
		return out, errors.New("communication exceeds 8 KiB")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		return out, err
	}
	if _, exists := fields["question_state"]; exists {
		return out, errors.New("question state comes from recorded replies")
	}
	for _, field := range []string{"disposition", "disposition_reason"} {
		if _, exists := fields[field]; exists {
			return out, errors.New("post resolution comes from its recorded disposition")
		}
	}
	for _, field := range []string{"library_item_id", "library_revision", "intent_source", "plan_source"} {
		if _, exists := fields[field]; exists {
			return out, errors.New("canonical source references are assigned by the server")
		}
	}
	if _, exists := fields["kind"]; exists {
		return out, errors.New("communication kind comes from its tool")
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&out); err != nil {
		return out, fmt.Errorf("communication payload: %w", err)
	}
	out.Kind = strings.TrimPrefix(string(tool), "factory.")
	// A retired worker presentation ID is accepted and ignored; only
	// controller-written events carry one.
	out.ComponentID = ""
	if err := out.Validate(); err != nil {
		return out, err
	}
	return out, nil
}

func (c FeedCommunication) Validate() error {
	if len(c.Commits) > 8 {
		return errors.New("at most 8 commits per post")
	}
	for _, commit := range c.Commits {
		if !ValidCommit(commit.Revision) || strings.TrimSpace(commit.Summary) == "" || utf8.RuneCountInString(commit.Summary) > 160 {
			return errors.New("commit needs a full revision and a summary of at most 160 characters")
		}
	}
	if s := c.Source; s != nil {
		if strings.TrimSpace(s.Path) == "" || len(s.Path) > 512 || strings.ContainsAny(s.Path, "\r\n\x00") || len(s.Revision) > 80 || s.StartLine < 1 || s.StartLine > 1000000 || strings.TrimSpace(s.Code) == "" || len(s.Code) > 3072 || len(s.After) > 3072 || strings.Count(s.Code, "\n") >= 32 || strings.Count(s.After, "\n") >= 32 || len(s.HighlightLines) > 32 {
			return errors.New("source excerpt needs a path, starting line and at most 32 lines / 3072 bytes per excerpt")
		}
		for _, line := range s.HighlightLines {
			if line < s.StartLine || line > s.StartLine+strings.Count(s.Code, "\n") {
				return errors.New("highlight line must occur in the source excerpt")
			}
		}
	}
	for name, value := range map[string]string{"title": c.Title, "summary": c.Summary} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("communication needs %s", name)
		}
	}
	if utf8.RuneCountInString(c.Title) > 160 {
		return errors.New("communication title exceeds 160 characters")
	}
	for name, value := range map[string]string{"summary": c.Summary, "question": c.Question, "resolution": c.Resolution, "scope": c.Scope, "decision": c.Decision, "rationale": c.Rationale, "before": c.Before, "after": c.After} {
		if utf8.RuneCountInString(value) > 2048 {
			return fmt.Errorf("communication %s exceeds 2048 characters", name)
		}
	}
	for _, list := range [][]string{c.EvidenceRefs, c.Options, c.Completed, c.Remaining, c.Attempts, c.Alternatives} {
		if len(list) > 24 {
			return errors.New("communication list exceeds 24 entries")
		}
		for _, value := range list {
			if strings.TrimSpace(value) == "" || utf8.RuneCountInString(value) > 512 {
				return errors.New("communication list entry must contain 1–512 characters")
			}
		}
	}
	if len(c.Items) > 24 {
		return errors.New("communication items exceed 24 entries")
	}
	ids := map[string]bool{}
	for _, item := range c.Items {
		if item.ID != "" {
			if len(item.ID) > 64 || ids[item.ID] {
				return errors.New("graph IDs must be unique and at most 64 characters")
			}
			ids[item.ID] = true
		}
	}
	for _, item := range c.Items {
		if len(item.DependsOn) > 24 {
			return errors.New("too many graph dependencies")
		}
		for _, dep := range item.DependsOn {
			if dep == item.ID || !ids[dep] {
				return errors.New("graph dependency must name another item")
			}
		}
		if strings.TrimSpace(item.Label) == "" || utf8.RuneCountInString(item.Label) > 160 || utf8.RuneCountInString(item.Detail) > 512 || utf8.RuneCountInString(item.State) > 64 {
			return errors.New("communication item exceeds its text bound")
		}
	}
	if len(c.Artifacts) > 8 {
		return errors.New("at most 8 artifacts per post")
	}
	for _, a := range c.Artifacts {
		if !ValidID(a.ID) {
			return errors.New("artifact ID is required")
		}
	}
	if c.ParentID != "" && !ValidID(c.ParentID) {
		return errors.New("parent_id must name a feed post")
	}
	// Revisions and challenges ride the learned outlet: the server
	// assigns library_item_id/library_revision, so a worker names its
	// target explicitly and the insert transaction checks the expected
	// revision. Decisions amend through their own owner (R32.5).
	if c.RevisesItem != "" && !ValidID(c.RevisesItem) {
		return errors.New("revises_item must name a library item")
	}
	if c.Challenge && c.RevisesItem == "" {
		return errors.New("challenge needs revises_item")
	}
	if c.Kind != "learned" && (c.RevisesItem != "" || c.Challenge) {
		return errors.New("only learned amends knowledge entries")
	}
	// The decision owner is the person/role/system that made the
	// choice, distinct from the worker logging it: a worker records
	// its own technical choice by leaving the owner blank, or names
	// the authorized owner it references. The revision actor always
	// stays the logging worker.
	if c.DecisionOwner != "" {
		if c.Kind != "record_decision" {
			return errors.New("only record_decision names a decision owner")
		}
		owner := strings.TrimSpace(c.DecisionOwner)
		if owner == "" || utf8.RuneCountInString(owner) > 160 || strings.ContainsAny(owner, "\r\n\x00") {
			return errors.New("decision owner must be 1–160 characters without line breaks")
		}
	}
	switch c.Severity {
	case "", "info", "warning", "critical":
	default:
		return errors.New("unknown communication severity")
	}
	required := func(value, field string) error {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s needs %s", c.Kind, field)
		}
		return nil
	}
	switch c.Kind {
	case "ask":
		return required(c.Question, "question")
	case "answer", "reply":
		return required(c.ParentID, "parent_id")
	case "flag":
		return required(c.Severity, "severity")
	case "learned":
		if len(c.EvidenceRefs) == 0 {
			return errors.New("learned needs evidence_refs")
		}
		if c.RevisesItem != "" && c.ExpectedRevision < 1 {
			return errors.New("amendment needs the current revision")
		}
		return required(c.Scope, "scope")
	case "suggest":
		return required(c.Rationale, "rationale")
	case "partial":
		if len(c.Completed) == 0 && len(c.Remaining) == 0 {
			return errors.New("partial needs completed or remaining work")
		}
	case "blocked":
		return required(c.Resolution, "resolution")
	case "stuck":
		if len(c.Attempts) == 0 {
			return errors.New("stuck needs attempted approaches")
		}
	case "record_decision":
		if err := required(c.Decision, "decision"); err != nil {
			return err
		}
		return required(c.Rationale, "rationale")
	default:
		return errors.New("unknown communication kind")
	}
	return nil
}
