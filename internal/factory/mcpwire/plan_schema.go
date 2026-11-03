package mcpwire

import (
	"github.com/google/jsonschema-go/jsonschema"

	"github.com/levitateos/sodaos/internal/factory"
)

// Investigation discovery uses the same typed carrier as settlement, with
// the domain's identity and graph vocabulary visible before submission.
func investigationPlanSchema() *jsonschema.Schema {
	schema := mcpCarrier[factory.InvestigationPlan]()
	schema.Description = factory.InvestigationPlanGuidance
	// These bindings are supplied by bindIssuePlan after SDK input validation.
	// The worker supplies the plan, not copies of server-owned assignment data.
	serverFields := map[string]bool{
		"attempt_root": true, "source_commit": true, "integration_commit": true,
		"scope_digest": true, "input_digest": true, "preparation": true, "digest": true,
	}
	required := schema.Required[:0]
	for _, field := range schema.Required {
		if !serverFields[field] {
			required = append(required, field)
		}
	}
	schema.Required = required
	hex := func(pattern, description string) *jsonschema.Schema {
		return &jsonschema.Schema{Type: "string", Pattern: pattern, Description: description}
	}
	schema.Properties["id"] = hex("^[a-f0-9]{32}$", "New immutable plan revision identity: exactly 32 lowercase hexadecimal digits, never a readable slug.")
	schema.Properties["attempt_root"] = hex("^[a-f0-9]{32}$", "SodaOS fills this from the assignment when you finish.")
	for _, field := range []string{"source_commit", "integration_commit"} {
		schema.Properties[field] = hex("^[a-f0-9]{40}$", "SodaOS fills source_commit, and the initial integration_commit, from the assignment when you finish; this is not a new candidate commit.")
	}
	schema.Properties["scope_digest"] = hex("^[a-f0-9]{64}$", "SodaOS fills this from the assignment when you finish.")
	schema.Properties["input_digest"] = hex("^[a-f0-9]{64}$", "SodaOS fills this from the assignment when you finish.")
	schema.Properties["digest"] = hex("^[a-f0-9]{64}$", "SodaOS computes this plan digest when you finish.")
	schema.Properties["preparation"] = hex("^f[a-f0-9]{24}$", "SodaOS fills this from the assignment when you finish.")
	zero := float64(0)
	schema.Properties["generation"].Minimum = &zero
	schema.Properties["prerequisite_digests"].Items = hex("^[a-f0-9]{64}$", "Echo the prerequisite binding digests in the admitted accepted context.")

	key := func(description string) *jsonschema.Schema {
		return hex("^[a-z][a-z0-9_-]{0,31}$", description)
	}
	task := mcpCarrier[factory.PlanTask]()
	for _, field := range []string{"id", "lane"} {
		task.Properties[field] = key("Plan-local task or lane key, at most 32 characters.")
	}
	for _, field := range []string{"depends_on", "check_ids"} {
		task.Properties[field].Items = key("Reference an earlier task key or a declared plan-local check key.")
	}
	for _, field := range []string{"outputs", "acceptance_refs"} {
		task.Properties[field].MinItems = ptrInt(1)
	}
	task.Properties["owned_paths"].MaxItems = ptrInt(factory.MaxTaskOwnership)
	task.Properties["owned_paths"].Description = "Exclusive clean repository-relative files or directory prefixes ending in /. No globs, absolute paths, parent escapes or .git/.soda-home paths."
	schema.Properties["tasks"].Items = task
	schema.Properties["tasks"].MinItems = ptrInt(1)
	schema.Properties["tasks"].MaxItems = ptrInt(factory.MaxPlanTasks)

	lane := mcpCarrier[factory.PlanLane]()
	lane.Properties["id"] = key("Plan-local lane key.")
	lane.Properties["task_ids"].Items = key("Declared task key. Each task belongs to exactly one lane.")
	schema.Properties["lanes"].Items = lane
	schema.Properties["lanes"].MinItems = ptrInt(1)
	schema.Properties["lanes"].MaxItems = ptrInt(factory.MaxPlanLanes)

	check := mcpCarrier[factory.CheckSpec]()
	check.Properties["id"] = key("Plan-local alias such as native_check. A native CI context such as ci/check.sh is not a valid plan check key.")
	check.Properties["scope"] = &jsonschema.Schema{Type: "string", Enum: []any{factory.CheckScopeTask, factory.CheckScopeIntegration}, Description: "Exactly task or integration; never a task ID."}
	minimum, maximum := float64(factory.MinCheckTimeoutSec), float64(factory.MaxCheckTimeoutSec)
	check.Properties["timeout_seconds"].Minimum, check.Properties["timeout_seconds"].Maximum = &minimum, &maximum
	check.Properties["argv"].MinItems, check.Properties["argv"].MaxItems = ptrInt(1), ptrInt(factory.MaxCheckArgv)
	check.Properties["argv"].Description = "Direct executable and arguments justified by inspected prepared source, for example [\"/bin/sh\",\"check.sh\"] for an inspected script. Declare an interpreter explicitly when needed. This plan-local CheckSpec does not require Product Lead's whole-application verification_catalog or verification_manifest_proposal."
	check.Properties["cwd"].MinLength = ptrInt(1)
	check.Properties["cwd"].Description = "A clean repository-relative working directory, or . for the checkout root."
	schema.Properties["checks"].Items = check
	schema.Properties["checks"].MaxItems = ptrInt(factory.MaxPlanChecks)
	schema.Properties["inspected"].MaxItems = ptrInt(factory.MaxInspectedRefs)
	schema.Properties["inspected"].Items.Properties["commit"] = hex("^[a-f0-9]{40}$", "Exact commit whose file was inspected.")
	schema.Properties["acceptance_coverage"].MinItems = ptrInt(1)
	return schema
}
