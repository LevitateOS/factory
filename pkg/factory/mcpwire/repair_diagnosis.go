package mcpwire

import (
	"github.com/google/jsonschema-go/jsonschema"

	"github.com/levitateos/factory/pkg/factory"
)

func repairDiagnosisPayloadSchema() *jsonschema.Schema {
	schema := mcpCarrier[factory.RepairDiagnosis]()
	schema.Properties["state"].Enum = []any{"suspected", "supported", "contradicted", "incomplete"}
	return schema
}
