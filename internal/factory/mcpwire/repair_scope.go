package mcpwire

import (
	"github.com/google/jsonschema-go/jsonschema"
)

func repairScopePayloadSchema() *jsonschema.Schema {
	shape := contextPayloadSchema()
	shape.Properties["after"] = &jsonschema.Schema{Type: "integer", Description: "Continue this assignment's diagnosis and draft references after the previous next_after sequence."}
	shape.Properties["record"] = &jsonschema.Schema{Type: "string", MaxLength: ptrInt(32), Description: "Read one post ID from reassessment_scope.records as bounded JSON fragments."}
	shape.Properties["offset"] = &jsonschema.Schema{Type: "integer", Description: "Continue the selected post using record_page.next_offset. Concatenate content, then decode JSON."}
	return shape
}
