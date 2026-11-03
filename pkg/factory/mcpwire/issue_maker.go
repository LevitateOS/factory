package mcpwire

import (
	"errors"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/levitateos/factory/pkg/factory"
)

func IsIssueMaker(tool factory.MCPTool) bool {
	return tool == factory.MCPToolCreateIssue || tool == factory.MCPToolRefineIssue
}

func issueMakerPayloadSchema() *jsonschema.Schema {
	one, maxTitle, maxBody := 1, 255, 16384
	return &jsonschema.Schema{Type: "object", Properties: map[string]*jsonschema.Schema{
		"key": {Type: "string", Pattern: "^[a-z][a-z0-9_-]{0,31}$"}, "title": {Type: "string", MinLength: &one, MaxLength: &maxTitle}, "body": {Type: "string", MinLength: &one, MaxLength: &maxBody},
	}, Required: []string{"key", "title", "body"}, AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}}}
}

func checkIssueMakerPayload(payload map[string]any) error {
	key, _ := payload["key"].(string)
	title, _ := payload["title"].(string)
	body, _ := payload["body"].(string)
	if len(payload) != 3 || !factory.ValidPlanKey(key) || strings.TrimSpace(title) == "" || len(title) > 255 || strings.TrimSpace(body) == "" || len(body) > 16384 {
		return errors.New("supply only a stable key, title (255 bytes) and body (16384 bytes)")
	}
	return nil
}
