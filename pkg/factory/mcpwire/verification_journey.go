package mcpwire

import (
	"slices"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/levitateos/factory/pkg/factory"
)

func verificationJourneyPayloadSchema(tool factory.MCPTool) *jsonschema.Schema {
	switch tool {
	case factory.MCPToolRecordJourneyResult:
		schema := mcpCarrier[factory.VerificationJourneyResult]()
		filled := map[string]bool{"delivery_id": true, "input_digest": true, "source_commit": true, "preparation": true, "manifest_digest": true}
		schema.Required = slices.DeleteFunc(schema.Required, func(field string) bool { return filled[field] })
		for field := range filled {
			if property := schema.Properties[field]; property != nil {
				property.Description = serverFilled
			}
		}
		return schema
	case factory.MCPToolVerificationScope:
		return &jsonschema.Schema{Type: "object", Properties: map[string]*jsonschema.Schema{"after": {Type: "integer"}}, AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}}}
	default:
		return nil
	}
}
