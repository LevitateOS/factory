package mcpwire

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/project"
)

func mcpEnvelopeSchema(tool factory.MCPTool) *mcp.Tool {
	required := []string{"capability_id"}
	if !factory.MCPIsRead(tool) || tool == factory.MCPToolDiscussion || tool == factory.MCPToolReadLibraryItem || tool == factory.MCPToolPreviewRepair || tool == factory.MCPToolReadSource || tool == factory.MCPToolListSource {
		required = append(required, "payload")
	}
	schema := &jsonschema.Schema{
		Type: "object", Properties: map[string]*jsonschema.Schema{
			"capability_id": {Type: "string"}, "submission_id": {Type: "string", Description: "Omit for new work; Factory generates an identity. Reuse only for an exact retry of the same tool and payload."}, "payload": {Type: "object"},
		},
		Required: required, AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}},
	}
	if payload := mcpPayloadSchema(tool); payload != nil {
		schema.Properties["payload"] = payload
	}
	if tool == factory.MCPToolPublishArtifact || IsNativeTask(tool) || tool == factory.MCPToolReadSource || tool == factory.MCPToolListSource {
		delete(schema.Properties, "submission_id")
	}
	if tool == factory.MCPToolWorkspace {
		schema.Properties["submission_id"] = &jsonschema.Schema{Type: "string", Pattern: "^[a-f0-9]{32}$", Description: "Stable mutation identity; reuse only for an exact retry."}
		schema.If = &jsonschema.Schema{Properties: map[string]*jsonschema.Schema{
			"payload": {Properties: map[string]*jsonschema.Schema{"operation": {Enum: []any{"edit", "mkdir", "remove"}}}, Required: []string{"operation"}},
		}, Required: []string{"payload"}}
		schema.Then = &jsonschema.Schema{Required: []string{"submission_id"}}
	}
	return &mcp.Tool{Name: string(tool), Description: factoryToolDescription(tool), InputSchema: schema}
}

func mcpPayloadSchema(tool factory.MCPTool) *jsonschema.Schema {
	switch tool {
	case factory.MCPToolWorkspace:
		return &jsonschema.Schema{Type: "object", Properties: map[string]*jsonschema.Schema{
			"task_id": {Type: "string", Pattern: "^[a-z][a-z0-9_-]{0,31}$"}, "operation": {Type: "string", Enum: []any{"read", "edit", "mkdir", "remove"}},
			"path": {Type: "string", MinLength: ptrInt(1), MaxLength: ptrInt(1024)}, "offset": {Type: "integer"}, "limit": {Type: "integer"},
			"expected": {Type: "string"}, "replacement": {Type: "string"}, "expected_digest": {Type: "string"},
		}, Required: []string{"task_id", "operation", "path"}, AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}}}
	case factory.MCPToolReadSource, factory.MCPToolListSource:
		minLength := 1
		if tool == factory.MCPToolListSource {
			minLength = 0
		}
		shape := &jsonschema.Schema{
			Type: "object", Properties: map[string]*jsonschema.Schema{
				"path": {Type: "string", MinLength: ptrInt(minLength), MaxLength: ptrInt(project.MaxFactoryContextPathBytes)},
			}, Required: []string{"path"}, AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}},
		}
		if tool == factory.MCPToolReadSource {
			shape.Properties["offset"] = &jsonschema.Schema{Type: "integer", Description: "Byte offset; omit for the start or use page.next_offset from the preceding reply."}
			shape.Properties["limit"] = &jsonschema.Schema{Type: "integer", Description: "Maximum bytes of source text in this page, from 4 to 32768; defaults to 8192."}
		}
		return shape
	case factory.MCPToolPreviewRepair:
		return repairPreviewPayloadSchema()
	case factory.MCPToolRecordDiagnosis:
		return repairDiagnosisPayloadSchema()
	case factory.MCPToolDraftRepair:
		return repairDraftPayloadSchema()
	case factory.MCPToolReassessmentScope:
		return repairScopePayloadSchema()
	case factory.MCPToolContext:
		return contextPayloadSchema()
	case factory.MCPToolPublishArtifact:
		return artifactPayloadSchema()
	}
	if IsNativeTask(tool) {
		return &jsonschema.Schema{
			Type: "object", Properties: map[string]*jsonschema.Schema{
				"task_id": {Type: "string", Pattern: "^[a-z][a-z0-9_-]{0,31}$"},
			}, Required: []string{"task_id"}, AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}},
		}
	}
	if IsIssueMaker(tool) {
		return issueMakerPayloadSchema()
	}
	// Typed result schemas take precedence over the shared communication schema.
	for _, define := range []func(factory.MCPTool) *jsonschema.Schema{
		resolvePostPayloadSchema, verificationJourneyPayloadSchema, libraryPayloadSchema,
		rolePayloadSchema, inboxPayloadSchema, communicationPayloadSchema,
	} {
		if schema := define(tool); schema != nil {
			return schema
		}
	}
	return nil
}

// canonicalPayload renders deterministic canonical bytes for the
// payload map and enforces the 64 KiB result bound.
func CanonicalPayload(payload map[string]any) ([]byte, error) {
	if payload == nil {
		return nil, errors.New("missing tool payload: payload must be an object with this tool's fields")
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, errors.New("tool payload is not JSON: payload must be a JSON object")
	}
	if len(raw) > factory.MCPMaxSubmissionPayload {
		return nil, errors.New("tool payload exceeds 64 KiB")
	}
	return raw, nil
}

// mcpPayloadSpec is the one key definition per tool payload: required
// keys, documented optional keys and enum-constrained fields. The
// advertised schema is built from it and admission enforces it; the
// typed carriers inside are decoded by the settler (CheckMCPTerminalFinish
// runs that decode before anything is recorded). Unknown keys refuse.
type mcpPayloadSpec struct {
	required []string
	optional []string
	enums    map[string][]string
}

var mcpMutationPayloads = map[factory.MCPTool]mcpPayloadSpec{
	factory.MCPToolFinishIncomplete: {
		required: []string{"outcome", "cause"},
		optional: []string{"cause_detail", "evidence_refs"},
		enums:    map[string][]string{"outcome": {"blocked", "failed", "cancelled"}},
	},
	factory.MCPToolFinishProductPlan: {
		required: []string{"candidates", "evidence_refs", "coverage"},
		optional: []string{"questions", "verification_manifest_proposal", "walkthroughs"},
	},
	factory.MCPToolFinishIssuePlan: {
		required: []string{"evidence_refs", "approach", "tasks"},
		optional: []string{"plan_review"},
	},
	factory.MCPToolFinishImplement: {
		optional: []string{"candidate"},
	},
	factory.MCPToolFinishReview: {
		required: []string{"conclusion", "evidence_refs"},
		optional: []string{"tuple", "coverage", "carried_assessments", "findings"},
		enums:    map[string][]string{"conclusion": {"clear", "findings", "incomplete"}},
	},
	factory.MCPToolFinishCorrection: {
		required: []string{"candidate", "dispositions", "evidence_refs"},
		optional: []string{"finding_digest", "fix_evidence"},
	},
	factory.MCPToolFinishReassessment: {
		required: []string{"subject", "report"},
		optional: []string{"input_digest"},
	},
	factory.MCPToolFinishAppVerify: {
		required: []string{"outcome", "coverage"},
		optional: []string{"delivery_id", "input_digest", "check_receipts", "artifact_refs", "failures", "evidence_refs"},
		enums:    map[string][]string{"outcome": {"verified", "failed", "incomplete"}},
	},
	factory.MCPToolSubmitTaskOutput: {
		required: []string{"task_id", "evidence_refs"},
		optional: []string{"manifest_digest", "output_digest", "native_child_ref"},
	},
}

// payloadKeyKnown reports whether the spec names the key. A retired
// presentation ID from an old worker is accepted and ignored.
func (spec mcpPayloadSpec) payloadKeyKnown(key string) bool {
	if key == "component_id" {
		return true
	}
	for _, list := range [][]string{spec.required, spec.optional} {
		for _, want := range list {
			if key == want {
				return true
			}
		}
	}
	return false
}

func checkPayloadKeys(tool factory.MCPTool, payload map[string]any, spec mcpPayloadSpec) error {
	for key := range payload {
		if !spec.payloadKeyKnown(key) {
			return fmt.Errorf("unknown payload field %q: %s takes %s", key, tool, strings.Join(append(append([]string(nil), spec.required...), spec.optional...), ", "))
		}
	}
	return nil
}

func checkPayloadRequired(payload map[string]any, spec mcpPayloadSpec) error {
	for _, want := range spec.required {
		value, ok := payload[want]
		if !ok {
			return fmt.Errorf("missing payload field %q: this tool requires %s", want, strings.Join(spec.required, ", "))
		}
		if text, ok := value.(string); ok && (text == "" || len(text) > factory.MCPMaxReasonChars) {
			return fmt.Errorf("payload field %q out of bound: it must be a non-empty string of at most %d characters", want, factory.MCPMaxReasonChars)
		}
	}
	return nil
}

func checkPayloadEnums(payload map[string]any, spec mcpPayloadSpec) error {
	for field, allowed := range spec.enums {
		text, _ := payload[field].(string)
		admitted := false
		for _, want := range allowed {
			if text == want {
				admitted = true
			}
		}
		if !admitted {
			return fmt.Errorf("payload field %q carries an unsupported value %.64q: use one of %s", field, text, strings.Join(allowed, ", "))
		}
	}
	return nil
}

// CheckPayload enforces the existing strict per-tool key and enum contract.
// Typed content and executable/workflow adoption retain their domain owners.
func CheckPayload(tool factory.MCPTool, payload map[string]any) error {
	if IsIssueMaker(tool) {
		return checkIssueMakerPayload(payload)
	}
	spec, ok := mcpMutationPayloads[tool]
	if !ok {
		return fmt.Errorf("unsupported factory tool %q: call one of the tools this server lists", tool)
	}
	if err := checkPayloadKeys(tool, payload, spec); err != nil {
		return err
	}
	if err := checkPayloadRequired(payload, spec); err != nil {
		return err
	}
	return checkPayloadEnums(payload, spec)
}
