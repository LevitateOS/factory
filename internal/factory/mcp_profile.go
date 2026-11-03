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
	switch profile {
	case MCPProfileIssueMaker:
		return MCPToolFinishProductPlan
	case MCPProfileProductLead:
		return MCPToolFinishProductPlan
	case MCPProfileArchitect:
		return MCPToolFinishIssuePlan
	case MCPProfileBuilder:
		return MCPToolFinishImplement
	case MCPProfileInspector:
		return MCPToolFinishReview
	case MCPProfileFixer:
		return MCPToolFinishCorrection
	case MCPProfileStrategist:
		return MCPToolFinishReassessment
	case MCPProfileTestPilot:
		return MCPToolFinishAppVerify
	default:
		return MCPToolFinishIncomplete
	}
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

// MCPTools lists shared reads and communication before role work/results.
var MCPTools = []MCPTool{
	MCPToolCreateIssue,
	MCPToolRefineIssue,
	MCPToolContext,
	MCPToolEvidence,
	MCPToolStatus,
	MCPToolInbox,
	MCPToolDiscussion,
	MCPToolSearchLibrary,
	MCPToolReadLibraryItem,
	MCPToolAsk,
	MCPToolAnswer,
	MCPToolReply,
	MCPToolFlag,
	MCPToolLearned,
	MCPToolSuggest,
	MCPToolPartial,
	MCPToolBlocked,
	MCPToolStuck,
	MCPToolRecordDecision,
	MCPToolPublishArtifact,
	MCPToolSystemOneNoul,
	MCPToolSystemOneChoice,
	MCPToolSystemOneScore,
	MCPToolFinishIncomplete,
	MCPToolFinishProductPlan,
	MCPToolFinishIssuePlan,
	MCPToolFinishImplement,
	MCPToolFinishReview,
	MCPToolFinishCorrection,
	MCPToolFinishReassessment,
	MCPToolPreviewRepair,
	MCPToolReassessmentScope,
	MCPToolRecordDiagnosis,
	MCPToolDraftRepair,
	MCPToolVerificationEvidence,
	MCPToolVerificationScope,
	MCPToolRecordJourneyResult,
	MCPToolFinishAppVerify,
	MCPToolSubmitTaskOutput,
	MCPToolStartTask,
	MCPToolReleaseOutput,
	MCPToolResolvePost,
}

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

// mcpReadTools are bound reads: they never launch work and never
// freeze the assignment.
var mcpReadTools = map[MCPTool]bool{
	MCPToolPreviewRepair:        true,
	MCPToolReassessmentScope:    true,
	MCPToolVerificationEvidence: true,
	MCPToolVerificationScope:    true,
	MCPToolSearchLibrary:        true,
	MCPToolReadLibraryItem:      true,
	MCPToolContext:              true,
	MCPToolEvidence:             true,
	MCPToolStatus:               true,
	MCPToolInbox:                true,
	MCPToolDiscussion:           true,
}

// MCPIsRead reports whether the tool is a bound read.
func MCPIsRead(tool MCPTool) bool { return mcpReadTools[tool] }

// mcpTerminalTools end the assignment with one recorded proposal.
// Exactly one terminal identity wins per assignment (see
// mcp_submission.go); a second identity with conflicting terminal
// content refuses.
var mcpTerminalTools = map[MCPTool]bool{
	MCPToolFinishIncomplete:   true,
	MCPToolFinishProductPlan:  true,
	MCPToolFinishIssuePlan:    true,
	MCPToolFinishImplement:    true,
	MCPToolFinishReview:       true,
	MCPToolFinishCorrection:   true,
	MCPToolFinishReassessment: true,
	MCPToolFinishAppVerify:    true,
}

// MCPIsTerminal reports whether the tool records a terminal proposal.
func MCPIsTerminal(tool MCPTool) bool { return mcpTerminalTools[tool] }

// mcpToolProfiles is the admitted caller set per tool. All
// profiles read; finish_incomplete is the every-parent terminal;
// typed finishes and task output stay with their role.
var mcpToolProfiles = map[MCPTool]map[MCPProfile]bool{
	MCPToolCreateIssue:          singleMCPProfile(MCPProfileIssueMaker),
	MCPToolRefineIssue:          singleMCPProfile(MCPProfileIssueMaker),
	MCPToolVerificationEvidence: singleMCPProfile(MCPProfileTestPilot),
	MCPToolVerificationScope:    singleMCPProfile(MCPProfileTestPilot),
	MCPToolRecordJourneyResult:  singleMCPProfile(MCPProfileTestPilot),
	MCPToolSearchLibrary:        allMCPProfiles(),
	MCPToolReadLibraryItem:      allMCPProfiles(),
	MCPToolPublishArtifact:      allMCPProfiles(),
	MCPToolSystemOneNoul:        allMCPProfiles(),
	MCPToolSystemOneChoice:      allMCPProfiles(),
	MCPToolSystemOneScore:       allMCPProfiles(),
	MCPToolContext:              allMCPProfiles(),
	MCPToolEvidence:             allMCPProfiles(),
	MCPToolStatus:               allMCPProfiles(),
	MCPToolInbox:                allMCPProfiles(),
	MCPToolDiscussion:           allMCPProfiles(),
	MCPToolAsk:                  allMCPProfiles(),
	MCPToolAnswer:               allMCPProfiles(),
	MCPToolReply:                allMCPProfiles(),
	MCPToolFlag:                 allMCPProfiles(),
	MCPToolLearned:              allMCPProfiles(),
	MCPToolSuggest:              allMCPProfiles(),
	MCPToolPartial:              allMCPProfiles(),
	MCPToolBlocked:              allMCPProfiles(),
	MCPToolStuck:                allMCPProfiles(),
	MCPToolRecordDecision:       allMCPProfiles(),
	MCPToolFinishIncomplete:     allMCPProfiles(),
	MCPToolFinishProductPlan:    {MCPProfileProductLead: true, MCPProfileIssueMaker: true},
	MCPToolFinishIssuePlan:      singleMCPProfile(MCPProfileArchitect),
	MCPToolFinishImplement:      singleMCPProfile(MCPProfileBuilder),
	MCPToolFinishReview:         singleMCPProfile(MCPProfileInspector),
	MCPToolFinishCorrection:     singleMCPProfile(MCPProfileFixer),
	MCPToolFinishReassessment:   singleMCPProfile(MCPProfileStrategist),
	MCPToolPreviewRepair:        singleMCPProfile(MCPProfileStrategist),
	MCPToolReassessmentScope:    singleMCPProfile(MCPProfileStrategist),
	MCPToolRecordDiagnosis:      singleMCPProfile(MCPProfileStrategist),
	MCPToolDraftRepair:          singleMCPProfile(MCPProfileStrategist),
	MCPToolFinishAppVerify:      singleMCPProfile(MCPProfileTestPilot),
	MCPToolResolvePost:          allMCPProfiles(),
	MCPToolSubmitTaskOutput: {
		MCPProfileBuilder: true,
		MCPProfileFixer:   true,
	},
	MCPToolStartTask:     singleMCPProfile(MCPProfileBuilder),
	MCPToolReleaseOutput: singleMCPProfile(MCPProfileFixer),
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
	_, ok := mcpToolProfiles[tool]
	return ok
}

// MCPToolAdmitted reports whether the profile may call the tool.
// A tool absent from discovery for this profile still refuses here:
// callers cannot reach it by direct call.
func MCPToolAdmitted(profile MCPProfile, tool MCPTool) bool {
	admitted, ok := mcpToolProfiles[tool]
	if !ok {
		return false
	}
	return admitted[profile]
}

// MCPDiscovery lists the tools advertised to the profile: the four
// reads plus its admitted mutations. Transport serves exactly this
// set per capability; anything else refuses before admission.
func MCPDiscovery(profile MCPProfile) []MCPTool {
	var tools []MCPTool
	for _, tool := range MCPTools {
		if MCPToolAdmitted(profile, tool) {
			tools = append(tools, tool)
		}
	}
	return tools
}
