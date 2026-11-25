package mcpwire

import (
	"github.com/google/jsonschema-go/jsonschema"

	"github.com/levitateos/factory/pkg/factory"
)

// communicationPayloadSchema exposes the usable structured payload in tool
// discovery; server decoding enforces the same bounds on direct calls.
func communicationPayloadSchema(tool factory.MCPTool) *jsonschema.Schema {
	if !factory.MCPIsCommunication(tool) {
		return nil
	}
	props := map[string]*jsonschema.Schema{}
	for _, key := range []string{"title", "summary", "question", "resolution", "scope", "decision", "rationale", "parent_id", "before", "after"} {
		props[key] = &jsonschema.Schema{Type: "string"}
	}
	props["severity"] = &jsonschema.Schema{Type: "string", Enum: []any{"info", "warning", "critical"}}
	props["commits"] = &jsonschema.Schema{Type: "array", MaxItems: ptrInt(8), Description: "Actual saved commits, in chronological order. Full revision and a short change summary. Recording commits does not claim they passed review.", Items: &jsonschema.Schema{Type: "object", Properties: map[string]*jsonschema.Schema{"revision": {Type: "string"}, "summary": {Type: "string"}}, Required: []string{"revision", "summary"}, AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}}}}
	props["source"] = &jsonschema.Schema{Type: "object", Description: "Optional actual source evidence: path, revision, starting line and a short code excerpt. highlight_lines uses absolute line numbers. Supply after for a correction comparison. Reported evidence does not establish verification.", Properties: map[string]*jsonschema.Schema{
		"path": {Type: "string"}, "revision": {Type: "string"}, "start_line": {Type: "integer"}, "code": {Type: "string"}, "after": {Type: "string"}, "highlight_lines": {Type: "array", Items: &jsonschema.Schema{Type: "integer"}},
	}, Required: []string{"path", "start_line", "code"}, AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}}}
	for _, key := range []string{"evidence_refs", "options", "completed", "remaining", "attempts", "alternatives"} {
		props[key] = &jsonschema.Schema{Type: "array", Items: &jsonschema.Schema{Type: "string"}}
	}
	props["items"] = &jsonschema.Schema{Type: "array", Items: &jsonschema.Schema{Type: "object", Properties: map[string]*jsonschema.Schema{"id": {Type: "string"}, "depends_on": {Type: "array", Items: &jsonschema.Schema{Type: "string"}}, "label": {Type: "string"}, "detail": {Type: "string"}, "state": {Type: "string"}}, Required: []string{"label"}, AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}}}}
	props["artifacts"] = &jsonschema.Schema{Type: "array", Items: &jsonschema.Schema{Type: "object", Properties: map[string]*jsonschema.Schema{"id": {Type: "string"}}, Required: []string{"id"}, AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}}}}
	required := []string{"title", "summary"}
	switch tool {
	case factory.MCPToolAsk:
		required = append(required, "question")
		props["answer_form"] = answerFormSchema()
	case factory.MCPToolAnswer, factory.MCPToolReply:
		required = append(required, "parent_id")
	case factory.MCPToolFlag:
		required = append(required, "severity")
	case factory.MCPToolLearned:
		required = append(required, "scope", "evidence_refs")
	case factory.MCPToolSuggest:
		required = append(required, "rationale")
	case factory.MCPToolBlocked:
		required = append(required, "resolution")
	case factory.MCPToolStuck:
		required = append(required, "attempts")
	case factory.MCPToolRecordDecision:
		required = append(required, "decision", "rationale")
	}
	return &jsonschema.Schema{Type: "object", Properties: props, Required: required, AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}}}
}

// answerFormSchema advertises a typed question's answer shape. The person
// answers in the feed with native controls; you receive the reply's typed
// answer and its plain-text body. factory.FeedQuestionForm.Validate enforces
// the same bounds on every call.
func answerFormSchema() *jsonschema.Schema {
	text := func(max int, description string) *jsonschema.Schema {
		return &jsonschema.Schema{Type: "string", MaxLength: ptrInt(max), Description: description}
	}
	return &jsonschema.Schema{Type: "object", Description: "Optional typed answer. Omit it for a free-text answer (the default). Labels are plain text on one line, without markup. pick_one: 2-6 options. yes_no: optional yes_means/no_means. score: integer low..high inside 0..10, optional low_label/high_label. pick_several: 2-8 options, optional min_picks/max_picks. value: label, optional unit, number=true for a number. Do not also send options.", Properties: map[string]*jsonschema.Schema{
		"kind": {Type: "string", Enum: []any{factory.QuestionPickOne, factory.QuestionYesNo, factory.QuestionScore, factory.QuestionPickSeveral, factory.QuestionValue}},
		"options": {Type: "array", MinItems: ptrInt(2), MaxItems: ptrInt(8), Items: &jsonschema.Schema{Type: "object", Properties: map[string]*jsonschema.Schema{
			"id": {Type: "string", Pattern: "^[a-z0-9][a-z0-9_-]{0,31}$"}, "label": text(80, "Option label."), "detail": text(240, "Optional line explaining the option."),
		}, Required: []string{"id", "label"}, AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}}}},
		"min_picks": {Type: "integer"}, "max_picks": {Type: "integer"},
		"yes_means": text(160, "What a yes leads to."), "no_means": text(160, "What a no leads to."),
		"low": {Type: "integer"}, "high": {Type: "integer"},
		"low_label": text(40, "Name of the lowest score."), "high_label": text(40, "Name of the highest score."),
		"label": text(80, "What the value is."), "unit": text(16, "Optional unit."), "number": {Type: "boolean"},
	}, Required: []string{"kind"}, AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}}}
}
