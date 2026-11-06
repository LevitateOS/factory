package factory

// D14 canonical profile/tool DTOs (FW38).
//
// One Factory MCP implementation serves role profiles with shared
// communication outlets and role-specific result tools. This file freezes the canonical names, the execution phase
// each profile serves, and which profile may call which tool. Admission
// (control), persistence (store) and transport (web/api) enforce this
// matrix; nothing else invents a profile or tool name.

// MCPProfile is one admitted Factory agent profile. Profile names are
// workflow roles, never native account identities: native
// coder/reviewer accounts remain the two execution identities (D14).
type MCPProfile string

const (
	MCPProfileIssueMaker  MCPProfile = "issue_maker"
	MCPProfileProductLead MCPProfile = "product_lead"
	MCPProfileArchitect   MCPProfile = "architect"
	MCPProfileBuilder     MCPProfile = "builder"
	MCPProfileInspector   MCPProfile = "inspector"
	MCPProfileFixer       MCPProfile = "fixer"
	MCPProfileStrategist  MCPProfile = "strategist"
	MCPProfileTestPilot   MCPProfile = "test_pilot"
)

// MCPProfiles lists the admitted profiles in contract order.
var MCPProfiles = []MCPProfile{
	MCPProfileProductLead,
	MCPProfileArchitect,
	MCPProfileBuilder,
	MCPProfileInspector,
	MCPProfileFixer,
	MCPProfileStrategist,
	MCPProfileTestPilot,
	MCPProfileIssueMaker,
}

// MCPProfileOfAssignment distinguishes intake from product planning while
// both use the existing funded, supervised decompose execution transport.
func MCPProfileOfAssignment(a Assignment) MCPProfile {
	if a.Phase == "decompose" && a.PlanningContext != nil && a.PlanningContext.IssueMaker != nil {
		return MCPProfileIssueMaker
	}
	if a.Phase == "repair" {
		return MCPProfileStrategist
	}
	for _, profile := range MCPProfiles {
		if MCPPhaseOf(profile) == a.Phase {
			return profile
		}
	}
	return ""
}

// MCPPhaseOf returns the execution phase a profile serves. Strategist
// serves reassess or repair depending on its admitted subject; both
// phases share the profile.
func MCPPhaseOf(profile MCPProfile) string {
	switch profile {
	case MCPProfileIssueMaker:
		return "decompose"
	case MCPProfileProductLead:
		return "decompose"
	case MCPProfileArchitect:
		return "investigate"
	case MCPProfileBuilder:
		return "implement"
	case MCPProfileInspector:
		return "review"
	case MCPProfileFixer:
		return "correct"
	case MCPProfileStrategist:
		return "reassess"
	case MCPProfileTestPilot:
		return "verify"
	default:
		return ""
	}
}

// MCPFinishToolOf returns the typed finish tool that records a profile's
// result. Every profile may also end with finish_incomplete.
func MCPFinishToolOf(profile MCPProfile) MCPTool {
	for _, definition := range mcpToolDefinitions {
		if definition.name != MCPToolFinishIncomplete && definition.mode&mcpTerminal != 0 && definition.profiles[profile] {
			return definition.name
		}
	}
	return MCPToolFinishIncomplete
}

// MCPTool is one tool in the shared role-filtered catalog.
type MCPTool string

const (
	MCPToolCreateIssue          MCPTool = "factory.create_issue"
	MCPToolRefineIssue          MCPTool = "factory.refine_issue"
	MCPToolVerificationEvidence MCPTool = "factory.verification_evidence"
	MCPToolSearchLibrary        MCPTool = "factory.search_library"
	MCPToolReadLibraryItem      MCPTool = "factory.read_library_item"
	MCPToolPublishArtifact      MCPTool = "factory.publish_artifact"
	MCPToolSystemOneNoul        MCPTool = "systemone.noul"
	MCPToolSystemOneChoice      MCPTool = "systemone.choice"
	MCPToolSystemOneScore       MCPTool = "systemone.score"
	MCPToolContext              MCPTool = "factory.context"
	MCPToolEvidence             MCPTool = "factory.evidence"
	MCPToolStatus               MCPTool = "factory.status"
	MCPToolInbox                MCPTool = "factory.inbox"
	MCPToolDiscussion           MCPTool = "factory.discussion"
	MCPToolAsk                  MCPTool = "factory.ask"
	MCPToolAnswer               MCPTool = "factory.answer"
	MCPToolReply                MCPTool = "factory.reply"
	MCPToolFlag                 MCPTool = "factory.flag"
	MCPToolLearned              MCPTool = "factory.learned"
	MCPToolSuggest              MCPTool = "factory.suggest"
	MCPToolPartial              MCPTool = "factory.partial"
	MCPToolBlocked              MCPTool = "factory.blocked"
	MCPToolStuck                MCPTool = "factory.stuck"
	MCPToolRecordDecision       MCPTool = "factory.record_decision"
	MCPToolFinishIncomplete     MCPTool = "factory.finish_incomplete"
	MCPToolFinishProductPlan    MCPTool = "factory.finish_product_plan"
	MCPToolFinishIssuePlan      MCPTool = "factory.finish_issue_plan"
	MCPToolFinishImplement      MCPTool = "factory.finish_implementation"
	MCPToolFinishReview         MCPTool = "factory.finish_review"
	MCPToolFinishCorrection     MCPTool = "factory.finish_correction"
	MCPToolFinishReassessment   MCPTool = "factory.finish_reassessment"
	MCPToolResolvePost          MCPTool = "factory.resolve_post"
	MCPToolFinishAppVerify      MCPTool = "factory.finish_app_verification"
	MCPToolVerificationScope    MCPTool = "factory.verification_scope"
	MCPToolPreviewRepair        MCPTool = "factory.preview_repair"
	MCPToolReassessmentScope    MCPTool = "factory.reassessment_scope"
	MCPToolRecordDiagnosis      MCPTool = "factory.record_diagnosis"
	MCPToolDraftRepair          MCPTool = "factory.draft_repair"
	MCPToolRecordJourneyResult  MCPTool = "factory.record_journey_result"
	MCPToolSubmitTaskOutput     MCPTool = "factory.submit_task_output"
	MCPToolStartTask            MCPTool = "factory.start_task"
	MCPToolReleaseOutput        MCPTool = "factory.release_output"
)

type mcpToolMode uint8

const (
	mcpRead mcpToolMode = 1 << iota
	mcpTerminal
	mcpDirect
)

type mcpToolDefinition struct {
	name     MCPTool
	profiles map[MCPProfile]bool
	mode     mcpToolMode
}

var mcpToolDefinitions = []mcpToolDefinition{
	{MCPToolCreateIssue, singleMCPProfile(MCPProfileIssueMaker), 0},
	{MCPToolRefineIssue, singleMCPProfile(MCPProfileIssueMaker), 0},
	{MCPToolContext, allMCPProfiles(), mcpRead},
	{MCPToolEvidence, allMCPProfiles(), mcpRead},
	{MCPToolStatus, allMCPProfiles(), mcpRead},
	{MCPToolInbox, allMCPProfiles(), mcpRead},
	{MCPToolDiscussion, allMCPProfiles(), mcpRead},
	{MCPToolSearchLibrary, allMCPProfiles(), mcpRead},
	{MCPToolReadLibraryItem, allMCPProfiles(), mcpRead},
	{MCPToolAsk, allMCPProfiles(), 0},
	{MCPToolAnswer, allMCPProfiles(), 0},
	{MCPToolReply, allMCPProfiles(), 0},
	{MCPToolFlag, allMCPProfiles(), 0},
	{MCPToolLearned, allMCPProfiles(), 0},
	{MCPToolSuggest, allMCPProfiles(), 0},
	{MCPToolPartial, allMCPProfiles(), 0},
	{MCPToolBlocked, allMCPProfiles(), 0},
	{MCPToolStuck, allMCPProfiles(), 0},
	{MCPToolRecordDecision, allMCPProfiles(), 0},
	{MCPToolPublishArtifact, allMCPProfiles(), mcpDirect},
	{MCPToolSystemOneNoul, allMCPProfiles(), 0},
	{MCPToolSystemOneChoice, allMCPProfiles(), 0},
	{MCPToolSystemOneScore, allMCPProfiles(), 0},
	{MCPToolFinishIncomplete, allMCPProfiles(), mcpTerminal},
	{MCPToolFinishProductPlan, map[MCPProfile]bool{MCPProfileProductLead: true, MCPProfileIssueMaker: true}, mcpTerminal},
	{MCPToolFinishIssuePlan, singleMCPProfile(MCPProfileArchitect), mcpTerminal},
	{MCPToolFinishImplement, singleMCPProfile(MCPProfileBuilder), mcpTerminal},
	{MCPToolFinishReview, singleMCPProfile(MCPProfileInspector), mcpTerminal},
	{MCPToolFinishCorrection, singleMCPProfile(MCPProfileFixer), mcpTerminal},
	{MCPToolFinishReassessment, singleMCPProfile(MCPProfileStrategist), mcpTerminal},
	{MCPToolPreviewRepair, singleMCPProfile(MCPProfileStrategist), mcpRead},
	{MCPToolReassessmentScope, singleMCPProfile(MCPProfileStrategist), mcpRead},
	{MCPToolRecordDiagnosis, singleMCPProfile(MCPProfileStrategist), 0},
	{MCPToolDraftRepair, singleMCPProfile(MCPProfileStrategist), 0},
	{MCPToolVerificationEvidence, singleMCPProfile(MCPProfileTestPilot), mcpRead},
	{MCPToolVerificationScope, singleMCPProfile(MCPProfileTestPilot), mcpRead},
	{MCPToolRecordJourneyResult, singleMCPProfile(MCPProfileTestPilot), 0},
	{MCPToolFinishAppVerify, singleMCPProfile(MCPProfileTestPilot), mcpTerminal},
	{MCPToolSubmitTaskOutput, map[MCPProfile]bool{
		MCPProfileBuilder: true,
		MCPProfileFixer:   true,
	}, 0},
	{MCPToolStartTask, singleMCPProfile(MCPProfileBuilder), mcpDirect},
	{MCPToolReleaseOutput, singleMCPProfile(MCPProfileFixer), mcpDirect},
	{MCPToolResolvePost, allMCPProfiles(), 0},
}

// MCPTools retains the public discovery order; admission uses the same definitions.
var MCPTools = func() []MCPTool {
	tools := make([]MCPTool, 0, len(mcpToolDefinitions))
	for _, definition := range mcpToolDefinitions {
		tools = append(tools, definition.name)
	}
	return tools
}()

var mcpToolContracts = func() map[MCPTool]mcpToolDefinition {
	contracts := make(map[MCPTool]mcpToolDefinition, len(mcpToolDefinitions))
	for _, definition := range mcpToolDefinitions {
		contracts[definition.name] = definition
	}
	return contracts
}()

// D14 byte/count bounds. Tool schemas reject unknown fields and
// enforce these limits; transport never silently truncates.
const (
	MCPMaxRequestEnvelopeBytes = 96 * 1024
	MCPMaxResultPayloadBytes   = 64 * 1024
	MCPMaxPageBytes            = 32 * 1024
	MCPMaxPageRefs             = 50
	MCPMaxConcurrentRequests   = 4
	MCPMaxCallsPerMinute       = 60
	MCPMaxInstructionBytes     = 16 * 1024
	MCPMaxConfigBytes          = 16 * 1024
	MCPMaxManifestBytes        = 64 * 1024
	MCPMaxLaunchEnvelopeBytes  = 1024 * 1024
	MCPMaxSubmissionPayload    = 64 * 1024
	MCPMaxReasonChars          = 1024
	MCPMaxRefChars             = 256
)

func MCPIsRead(tool MCPTool) bool { return mcpToolContracts[tool].mode&mcpRead != 0 }

func MCPIsTerminal(tool MCPTool) bool { return mcpToolContracts[tool].mode&mcpTerminal != 0 }

// Direct native requests and offset-based uploads use their own retained keys.
func MCPNeedsSubmission(tool MCPTool) bool {
	return mcpToolContracts[tool].mode&(mcpRead|mcpDirect) == 0
}

func allMCPProfiles() map[MCPProfile]bool {
	admitted := make(map[MCPProfile]bool, len(MCPProfiles))
	for _, profile := range MCPProfiles {
		admitted[profile] = true
	}
	return admitted
}

func singleMCPProfile(profile MCPProfile) map[MCPProfile]bool {
	return map[MCPProfile]bool{profile: true}
}

// MCPToolKnown reports whether the tool belongs to the catalog.
// Unknown tools fail server-side; discovery never advertises them.
func MCPToolKnown(tool MCPTool) bool {
	_, ok := mcpToolContracts[tool]
	return ok
}

// MCPToolAdmitted reports whether the profile may call the tool.
// A tool absent from discovery for this profile still refuses here:
// callers cannot reach it by direct call.
func MCPToolAdmitted(profile MCPProfile, tool MCPTool) bool {
	definition, ok := mcpToolContracts[tool]
	if !ok {
		return false
	}
	return definition.profiles[profile]
}

// Discovery never grants admission to a tool excluded from this profile.
func MCPDiscovery(profile MCPProfile) []MCPTool {
	var tools []MCPTool
	for _, tool := range MCPTools {
		if MCPToolAdmitted(profile, tool) {
			tools = append(tools, tool)
		}
	}
	return tools
}
