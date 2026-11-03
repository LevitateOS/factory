package mcpwire

import (
	"github.com/google/jsonschema-go/jsonschema"

	"github.com/levitateos/sodaos/internal/factory"
)

func repairPreviewPayloadSchema() *jsonschema.Schema {
	return &jsonschema.Schema{Type: "object", Properties: map[string]*jsonschema.Schema{
		"report": {AnyOf: []*jsonschema.Schema{mcpCarrier[factory.ReassessmentReport](), mcpCarrier[factory.StallRepairReport](), mcpCarrier[factory.DeliveryRepairReport]()}},
	}, Required: []string{"report"}, AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}}}
}
