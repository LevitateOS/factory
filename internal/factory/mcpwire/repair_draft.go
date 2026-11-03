package mcpwire

import (
	"github.com/google/jsonschema-go/jsonschema"
)

func repairDraftPayloadSchema() *jsonschema.Schema {
	shape := repairPreviewPayloadSchema()
	shape.Properties["diagnosis_refs"] = &jsonschema.Schema{Type: "array", MaxItems: ptrInt(16), Items: &jsonschema.Schema{Type: "string", Pattern: "^diagnosis:[a-f0-9]{32}$"}, Description: "Optional exact diagnosis references recorded by this assignment under the same cause/input. A reference grants no repair authority."}
	return shape
}
