package mcpwire

import (
	"github.com/google/jsonschema-go/jsonschema"
)

func artifactPayloadSchema() *jsonschema.Schema {
	return &jsonschema.Schema{Type: "object", Properties: map[string]*jsonschema.Schema{
		"upload_id": {Type: "string", Description: "Your stable 32-hex upload ID. Reuse across chunks and advance offset using next_offset."}, "name": {Type: "string"}, "media_type": {Type: "string"}, "offset": {Type: "integer", Description: "Bytes already uploaded; starts at zero."}, "content_base64": {Type: "string", Description: "Base64 of at most 32768 bytes. Total file at most 8 MiB."}, "final": {Type: "boolean", Description: "True for last chunk: publish the artifact and its feed card."},
	}, Required: []string{"upload_id", "name", "media_type", "offset", "content_base64", "final"}, AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}}}
}
