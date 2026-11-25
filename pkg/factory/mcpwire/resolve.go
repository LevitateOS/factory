package mcpwire

import (
	"github.com/google/jsonschema-go/jsonschema"

	"github.com/levitateos/factory/pkg/factory"
)

func resolvePostPayloadSchema(tool factory.MCPTool) *jsonschema.Schema {
	if tool != factory.MCPToolResolvePost {
		return nil
	}
	return &jsonschema.Schema{Type: "object", Properties: map[string]*jsonschema.Schema{
		"post":       {Type: "string"},
		"resolution": {Type: "string", Enum: []any{"applied", "refused", "incorporated"}},
		"reason":     {Type: "string"},
	}, Required: []string{"post", "resolution"}, AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}}}
}

func resolvePostToolDescription(tool factory.MCPTool) string {
	if string(tool) != "factory.resolve_post" {
		return ""
	}
	return "Record one disposition for a suggestion or answer post: applied or refused for suggestions, incorporated for adopted answers. One disposition per post; it rides beside the immutable post."
}

// withdrawPayloadSchema advertises factory.withdraw; factory.DecodeWithdrawal
// enforces the same shape on every call and the store the authorship rules.
func withdrawPayloadSchema(tool factory.MCPTool) *jsonschema.Schema {
	if tool != factory.MCPToolWithdraw {
		return nil
	}
	return &jsonschema.Schema{Type: "object", Properties: map[string]*jsonschema.Schema{
		"post":       {Type: "string", Description: "The post to withdraw: one your role posted in this project."},
		"reason":     {Type: "string", MaxLength: ptrInt(400), Description: "Why it is withdrawn, plain text on one line."},
		"correction": {Type: "string", Description: "Optional: a later post of your role that corrects it. Post the correction first."},
	}, Required: []string{"post", "reason"}, AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}}}
}
