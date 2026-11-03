package mcpwire

import (
	"github.com/google/jsonschema-go/jsonschema"
)

func contextPayloadSchema() *jsonschema.Schema {
	return &jsonschema.Schema{Type: "object", Properties: map[string]*jsonschema.Schema{"cursor": {Type: "string", MaxLength: ptrInt(1024), Description: "Continuation from current_role_page.next_cursor for the same input snapshot. If the context changes, begin a fresh context read."}}, AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}}}
}
