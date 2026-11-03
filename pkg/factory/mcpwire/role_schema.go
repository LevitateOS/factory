package mcpwire

import (
	"github.com/google/jsonschema-go/jsonschema"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/control"
)

// rolePayloadSchema documents each existing result carrier at discovery. The
// structured schemas come from the same Go DTOs the settler decodes. Architect
// plans require their complete typed shape before freezing a terminal result.
func rolePayloadSchema(tool factory.MCPTool) *jsonschema.Schema {
	spec, ok := mcpMutationPayloads[tool]
	if !ok {
		return nil
	}
	props := map[string]*jsonschema.Schema{}
	for _, key := range spec.required {
		props[key] = &jsonschema.Schema{Description: "Required result field."}
	}
	for _, key := range spec.optional {
		props[key] = &jsonschema.Schema{}
	}
	set := func(key, description string, shape *jsonschema.Schema) {
		if _, ok := props[key]; !ok {
			return
		}
		props[key] = &jsonschema.Schema{Description: description}
		if shape != nil {
			props[key].AnyOf = []*jsonschema.Schema{shape.CloneSchemas(), {}}
		}
	}
	text := &jsonschema.Schema{Type: "string"}
	strings := &jsonschema.Schema{Type: "array", Items: &jsonschema.Schema{Type: "string"}}
	set("evidence_refs", "References to evidence actually inspected; never a claim that checks passed.", strings)
	set("candidate", "Exact candidate commit hash produced by this work. Publication and merge are independently verified.", text)
	set("input_digest", serverFilled, text)
	switch tool {
	case factory.MCPToolFinishIncomplete:
		set("cause", "Short cause code or explanation for ending without a complete result.", text)
		set("cause_detail", "Optional supporting explanation.", text)
	case factory.MCPToolFinishProductPlan:
		set("candidates", "One to three structured semantic graph candidates. Each contains key, rationale and full task metadata. String candidate names alone cannot settle a product plan. "+control.FinishProductPlanArgsGuidance, mcpCarrier[[]factory.SemanticCandidate]())
		set("coverage", "Explain how the candidates cover the accepted requested outcomes.", strings)
		set("questions", "Any unresolved product questions; required unresolved questions prevent executable adoption.", strings)
		set("verification_manifest_proposal", "Optional structured proposal using approved check catalog references; separately validated and adopted. "+factory.ApplicationBundleGuidance, mcpCarrier[factory.VerificationManifestProposal]())
		props["walkthroughs"] = mcpCarrier[factory.ProposalContent]()
		props["walkthroughs"].Description = "Required for newly staged intent plans: author two concrete product walkthroughs using producer soda-planning-agent-v1, the exact admitted Content facts/identity/exclusions, and every resolved-scope ID from Head in each alternative's idea_ids (the explicit nonempty EffectiveOutcomes verbatim, else the accept/edit idea IDs in revision order; pending/declined never resolve). JEV compares and checks this authored prose separately. Follow the staged product walkthrough instructions. " + control.FinishProductPlanArgsGuidance
	case factory.MCPToolFinishIssuePlan:
		set("approach", "Approach chosen after source investigation; must match tasks.approach.", text)
		props["tasks"] = investigationPlanSchema()
	case factory.MCPToolFinishImplement:
		set("candidate", "Filled by SodaOS from the committed final integration checkpoint; send the exact candidate commit only when the run has no integration checkpoint.", text)
	case factory.MCPToolFinishReview:
		set("coverage", serverFilled, text)
		set("tuple", serverFilled, &jsonschema.Schema{Type: "object", Properties: map[string]*jsonschema.Schema{"cycle_id": {Type: "string"}, "head": {Type: "string"}, "base_oid": {Type: "string"}}, Required: []string{"cycle_id", "head", "base_oid"}, AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}}})
		set("findings", "Structured findings with actionable outcomes and evidence. A string summary cannot replace these records.", mcpCarrier[[]factory.Finding]())
		set("carried_assessments", "Disposition/evidence for each carried finding in this review cycle.", mcpCarrier[[]factory.FindingAssessment]())
	case factory.MCPToolFinishCorrection:
		set("finding_digest", serverFilled, text)
		set("dispositions", "A structured disposition for each input finding; do not silently omit unresolved findings.", mcpCarrier[[]factory.CorrectionDisposition]())
		set("fix_evidence", "Relevant changed paths and exact commits for each fixed code finding. Verification-only findings use passing native check evidence instead. These are nominations, independently verified before publication.", mcpCarrier[[]factory.CorrectionFixEvidence]())
	case factory.MCPToolFinishReassessment:
		set("subject", "The admitted reassessment/repair subject from context.", text)
		outcome := &jsonschema.Schema{Type: "string", Enum: []any{factory.RepairOutcomeRepair, factory.RepairOutcomeVerifiedWait, factory.RepairOutcomeNoChange, factory.RepairOutcomeIncomplete}, Description: "repair proposes authorized graph changes; verified_wait records an evidenced wait; no_change completes diagnosis without graph changes; incomplete names missing facts or authority. Unresolved stalls remain held for owner attention."}
		stall := mcpCarrier[factory.StallRepairReport]()
		stall.Properties["outcome"] = outcome.CloneSchemas()
		report := &jsonschema.Schema{AnyOf: []*jsonschema.Schema{mcpCarrier[factory.ReassessmentReport](), stall, mcpCarrier[factory.DeliveryRepairReport]()}}
		set("report", "Full typed ReassessmentReport, StallRepairReport or DeliveryRepairReport, matching the assigned phase/subject. This proposes a change; it does not apply it.", report)
		// Enforce the stall outcome even through the existing carrier fallback:
		// an invalid value must be corrected before an immutable terminal is
		// recorded. Other reassessment/repair subjects keep their own contract.
		props["report"].If = &jsonschema.Schema{Type: "object", Required: []string{"stall_id"}}
		props["report"].Then = &jsonschema.Schema{Type: "object", Properties: map[string]*jsonschema.Schema{"outcome": outcome}, Required: []string{"outcome"}}
	case factory.MCPToolFinishAppVerify:
		set("delivery_id", serverFilled, text)
		set("evidence_refs", "Inspected evidence; journey:<post ID> must resolve to this assignment's exact recorded observation. Verified claims may cite only passed journeys. These references never replace trusted check receipts.", strings)
		set("coverage", "Structured outcome coverage for the assigned delivery.", mcpCarrier[[]factory.ReassessmentCoverage]())
		set("check_receipts", "Observed typed receipts for approved checks; required to settle verified or failed.", mcpCarrier[[]factory.VerificationCheckReceipt]())
		set("artifact_refs", "Content digests from the publish response of this assignment's complete artifacts. Settlement resolves each digest back to the assignment and fences unknown, foreign or unfinished citations.", strings)
		set("failures", "Concrete failed checks with detail.", mcpCarrier[[]factory.CheckFailure]())
	case factory.MCPToolSubmitTaskOutput:
		set("task_id", "Task ID in the admitted execution manifest.", text)
		set("manifest_digest", serverFilled, text)
		set("output_digest", serverFilled, text)
		set("native_child_ref", serverFilled, text)

	}
	for field, values := range spec.enums {
		enums := make([]any, len(values))
		for i, value := range values {
			enums[i] = value
		}
		props[field] = &jsonschema.Schema{Type: "string", Enum: enums, Description: "Recorded result outcome; downstream effects require their independent validation."}
	}
	return &jsonschema.Schema{Type: "object", Properties: props, Required: spec.required, AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}}}
}

func mcpCarrier[T any]() *jsonschema.Schema {
	schema, err := jsonschema.For[T](nil)
	if err != nil {
		panic("Factory result schema cannot be described: " + err.Error())
	}
	return schema
}

// serverFilled describes a binding SodaOS fills from the assignment.
const serverFilled = "Filled by SodaOS from this assignment; omit it."
