package mcpwire

import (
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/levitateos/sodaos/internal/factory"
)

func systemOneTool(tool factory.MCPTool, capability *factory.MCPCapability) *mcp.Tool {
	kind := strings.TrimPrefix(string(tool), "systemone.")
	// instructions/criteria stay optional in the schema so a batch can
	// supply questions instead; the handler enforces the required
	// fields per invocation shape before calling the provider.
	schema := &jsonschema.Schema{Type: "object", Properties: map[string]*jsonschema.Schema{"state": {}, "instructions": {Types: []string{"string", "object", "array"}}, "criteria": {}}, Required: []string{"state"}, AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}}}
	schema.Properties["questions"] = &jsonschema.Schema{Type: "object", Description: "Up to 8 independent same-primitive questions over the shared state, answered in one round trip. Mutually exclusive with instructions/criteria.", AdditionalProperties: &jsonschema.Schema{Type: "object", Properties: map[string]*jsonschema.Schema{"instructions": {}, "criteria": {}}, Required: []string{"instructions"}}}
	schema.Properties["consultation_group"] = &jsonschema.Schema{Type: "string", Description: "Optional shared 32-character lowercase hex identity for three separate consultations supporting the same decision. Grouping affects discussion display only."}
	description := "Ask a generic System One " + kind + " judgment using supplied state. Records the question and result in your work post and returns their comment identity. Advice does not authorize or implement changes. Never include credentials. Pass questions to batch up to 8 independent same-primitive judgments over the shared state in one round trip. Follow with your interpretation and resulting decision as a reply to consultation.post_id."
	switch kind {
	case "noul":
		schema.Properties["criteria"] = &jsonschema.Schema{Type: "object", Properties: map[string]*jsonschema.Schema{"true": {}, "false": {}}, AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}}}
		description += " Noul is probability of yes from 0 to 1."
	case "choice":
		schema.Properties["criteria"] = &jsonschema.Schema{Type: "object", Description: "2–255 named alternatives with their descriptions."}
	case "score":
		schema.Properties["criteria"] = &jsonschema.Schema{Type: "array", Items: &jsonschema.Schema{Types: []string{"string", "object", "array"}}, MinItems: ptrInt(2), MaxItems: ptrInt(10), Description: "Ordered descriptions from lowest to highest; response score is between 0 and level count minus one."}
	}
	if capability == nil {
		schema.Properties["capability_id"] = &jsonschema.Schema{Type: "string"}
		schema.Required = append(schema.Required, "capability_id")
	}
	return &mcp.Tool{Name: string(tool), Description: description, InputSchema: schema}
}
