package factory

// Context bundle identities for R31 bundle selection. Code builds the
// fixed bundles; Jev returns exactly one eligible ID. snapshot_hold is
// the deterministic fallback, never a usable bundle.
const (
	ContextBundleCurrent      = "current"
	ContextBundleAttempt      = "attempt"
	ContextBundleLineage      = "lineage"
	ContextBundleSnapshotHold = "snapshot_hold"
)

// Context bundle decision bounds. Only an eligible selected option at
// or above both bounds serves; anything else holds with attention.
// Proposed bound pending context-specific evaluation.
const (
	MinContextBundleProbability = 0.80
	MinContextBundleConfidence  = 0.80
)

// Context bundle hold reasons. Empty reason means serve.
const (
	ContextBundleHoldLowConfidence = "low_confidence"
	ContextBundleHoldIneligible    = "ineligible_selection"
)

// BundleEligibility reports which usable bundles have backing content:
// attempt needs own-attempt run continuity on the recorded assignment,
// lineage needs a linked parent plus its loaded record. Code enforces
// eligibility; Jev only foregrounds among eligible bundles.
func BundleEligibility(assignment Assignment, parentLoaded bool) (attempt, lineage bool) {
	attempt = len(assignment.RunHistory) > 0
	lineage = assignment.ParentAssignment != "" && parentLoaded
	return attempt, lineage
}

// AttemptContinuity returns the own-attempt run identities for the
// attempt bundle: opaque IDs only, never prompt bytes, history content
// or actor identity. Nil when the assignment carries no continuity.
func AttemptContinuity(assignment Assignment) []string {
	if len(assignment.RunHistory) == 0 {
		return nil
	}
	return append([]string(nil), assignment.RunHistory...)
}

// ApplyContextBundleAnswer maps one validated answer to its bundle.
// Jev advises and never decides whether a run starts: an eligible
// selected option at or above both bounds serves that bundle, and
// anything else (low confidence, an ineligible pick or snapshot_hold)
// serves the always-eligible current bundle instead of holding.
func ApplyContextBundleAnswer(answer PriorityAnswer, eligible map[string]bool) (bundle, reason string) {
	if !eligible[answer.Selected] || answer.Selected == ContextBundleSnapshotHold {
		return ContextBundleCurrent, ""
	}
	if answer.Confidence < MinContextBundleConfidence || answer.Probabilities[answer.Selected] < MinContextBundleProbability {
		return ContextBundleCurrent, ""
	}
	for option, probability := range answer.Probabilities {
		if option != answer.Selected && answer.Probabilities[answer.Selected] <= probability+0.000001 {
			return ContextBundleCurrent, ""
		}
	}
	return answer.Selected, ""
}

// ContextParentVisible grants only the two bounded parent disposition fields
// across an explicit same-actor, same-project/repository attempt relation. It
// grants no prompt, conversation, arbitrary history or evidence access.
func ContextParentVisible(child, parent Assignment) bool {
	return child.ParentAssignment != "" && parent.ID == child.ParentAssignment &&
		child.ActorID > 0 && parent.ActorID == child.ActorID && child.Repository > 0 &&
		parent.Repository == child.Repository && ValidProjectID(child.ProjectID) &&
		parent.ProjectID == child.ProjectID && ValidID(child.AttemptRoot) &&
		parent.AttemptRoot == child.AttemptRoot
}
