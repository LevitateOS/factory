package mcpwire

import (
	"github.com/google/jsonschema-go/jsonschema"

	"github.com/levitateos/factory/pkg/factory"
)

func inboxPayloadSchema(tool factory.MCPTool) *jsonschema.Schema {
	if tool != factory.MCPToolInbox && tool != factory.MCPToolDiscussion {
		return nil
	}
	schema := &jsonschema.Schema{Type: "object", Properties: map[string]*jsonschema.Schema{"cursor": {Type: "string"}}, AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}}}
	if tool == factory.MCPToolDiscussion {
		schema.Properties["post_id"] = &jsonschema.Schema{Type: "string"}
		schema.Required = []string{"post_id"}
	}
	return schema
}
