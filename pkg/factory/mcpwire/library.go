package mcpwire

import (
	"github.com/google/jsonschema-go/jsonschema"

	"github.com/levitateos/factory/pkg/factory"
)

func libraryToolDescription(tool factory.MCPTool) string {
	switch tool {
	case factory.MCPToolSearchLibrary:
		return "Search this project's reusable knowledge and recorded decisions. Use current_only for current evidence: excludes removed, superseded and retracted entries, while contested entries retain their unresolved state. Returns exact revisions, sources and a continuation cursor. Without current_only, superseded history remains discoverable."
	case factory.MCPToolReadLibraryItem:
		return "Read one project library item, its current revision and recent attributed revision history."
	}
	return ""
}

func libraryPayloadSchema(tool factory.MCPTool) *jsonschema.Schema {
	if !factory.MCPIsLibraryRead(tool) {
		return nil
	}
	text := func() *jsonschema.Schema { return &jsonschema.Schema{Type: "string"} }
	o := &jsonschema.Schema{Type: "object", Properties: map[string]*jsonschema.Schema{}, AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}}}
	if tool == factory.MCPToolReadLibraryItem {
		o.Properties["id"] = text()
		o.Required = []string{"id"}
		return o
	}
	for _, key := range []string{"query", "kind", "cursor"} {
		o.Properties[key] = text()
	}
	o.Properties["kind"].Enum = []any{"knowledge", "decision"}
	o.Properties["current_only"] = &jsonschema.Schema{Type: "boolean", Description: "Exclude removed, superseded and retracted entries before pagination. Contested records remain visible as unresolved evidence."}
	return o
}

func ptrInt(v int) *int { return &v }
