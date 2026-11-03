// Package mcpwire owns Factory worker tool descriptions, SDK schemas and strict
// payload contracts. A tool definition grants no runtime or workflow authority.
package mcpwire

import (
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/levitateos/sodaos/internal/factory"
)

// Tool returns a fresh SDK definition for the existing worker protocol. A bound
// capability removes model-supplied identity from Factory and System One tools;
// discovery/profile filtering and current admission remain with the server.
func Tool(tool factory.MCPTool, capability *factory.MCPCapability) *mcp.Tool {
	if IsSystemOne(tool) {
		return systemOneTool(tool, capability)
	}
	definition := mcpEnvelopeSchema(tool)
	if capability != nil {
		bindMCPToolSchema(definition, tool, *capability)
	}
	return definition
}

// ResultSchema projects the existing result carrier into current-role context.
// It describes reported content; settlement independently validates/adopts it.
func ResultSchema(tool factory.MCPTool) *jsonschema.Schema {
	if schema := rolePayloadSchema(tool); schema != nil {
		return schema
	}
	return libraryPayloadSchema(tool)
}
