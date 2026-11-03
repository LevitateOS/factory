package control

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strings"

	"github.com/levitateos/sodaos/internal/factory"
)

// correctionPlanBlocked reports one visible planning blocker: the accepted
// inputs carry no correction policy to derive controller-selected checks
// and ownership from, so the corrector waits instead of launching with
// agent-selected checks. The reason rides the publication wait, never a
// launch.
type correctionPlanBlocked struct {
	reason string
	detail string
}

func (b *correctionPlanBlocked) Error() string { return "correction planning blocked: " + b.reason }

// correctionPlanWaitReason resolves one derivation failure to its visible
// wait reason. Anything that is not a policy gap is a controller error and
// reports false so the caller refuses loudly instead of waiting.
func correctionPlanWaitReason(err error) (string, bool) {
	var blocked *correctionPlanBlocked
	if errors.As(err, &blocked) {
		return blocked.reason, true
	}
	return "", false
}

// deriveCorrectionPlan builds the controller-generated singleton correction
// task plan for one review cycle: a single task on a single lane carrying
// the immutable investigation plan's derived ownership and checks, with no
// dependencies and no interface handoffs, staged later with MaxChildren 0.
// Ownership is the sorted union of the investigation's task ownership;
// checks are the investigation's checks verbatim, so the corrector
// executes controller-selected verification, never agent-selected checks.
// An accepted-policy gap — a missing or unexecutable investigation plan,
// no derived checks, no policy checks, or ownership beyond the singleton
// bound — is a visible planning blocker, never a guessed plan. The
// current verdict supplies the finding keys the plan answers; verdicts
// without stable keys block as well.
func checkCorrectionPlanAdmission(attemptRoot, head, base, preparation, cycleID, findingDigest string, acceptance factory.Acceptance, investigation factory.InvestigationPlan, policyChecks []string) error {
	if !factory.ValidID(attemptRoot) || !factory.ValidCommit(head) || !factory.ValidCommit(base) ||
		!factory.ValidID(cycleID) || !factory.ValidDigest(findingDigest) || !factory.ValidPreparationRef(preparation) {
		return &correctionPlanBlocked{reason: "correction_plan_unavailable", detail: "correction lacks its exact cycle identity"}
	}
	if strings.TrimSpace(acceptance.ID) == "" {
		return &correctionPlanBlocked{reason: "correction_plan_unavailable", detail: "correction lacks its accepted issue"}
	}
	if err := investigation.Validate(); err != nil {
		return &correctionPlanBlocked{reason: "correction_plan_unavailable", detail: "recorded investigation plan is not admitted"}
	}
	if err := investigation.Executable(); err != nil {
		return &correctionPlanBlocked{reason: "correction_plan_unavailable", detail: "recorded investigation plan is not executable"}
	}
	if investigation.AttemptRoot != attemptRoot {
		return &correctionPlanBlocked{reason: "correction_plan_unavailable", detail: "recorded investigation plan belongs to another attempt"}
	}
	if len(investigation.Checks) == 0 {
		return &correctionPlanBlocked{reason: "correction_plan_unavailable", detail: "recorded investigation plan carries no checks"}
	}
	if len(policyChecks) == 0 {
		return &correctionPlanBlocked{reason: "correction_plan_unavailable", detail: "accepted policy carries no required checks"}
	}
	return nil
}

func deriveCorrectionOwnership(investigation factory.InvestigationPlan) ([]string, error) {
	owned := map[string]bool{}
	for _, task := range investigation.Tasks {
		for _, path := range task.OwnedPaths {
			owned[path] = true
		}
	}
	ownership := make([]string, 0, len(owned))
	for path := range owned {
		ownership = append(ownership, path)
	}
	sort.Strings(ownership)
	if len(ownership) > factory.MaxTaskOwnership {
		return nil, &correctionPlanBlocked{reason: "correction_plan_unavailable", detail: "derived correction ownership exceeds its bound"}
	}
	return ownership, nil
}

func dedupeCorrectionFindingKeys(findingKeys []string) []string {
	seen := map[string]bool{}
	keys := make([]string, 0, len(findingKeys))
	for _, key := range findingKeys {
		if !factory.ValidFindingKey(key) || seen[key] {
			continue
		}
		seen[key] = true
		keys = append(keys, key)
	}
	return keys
}

func deriveCorrectionPlan(acceptance factory.Acceptance, investigation factory.InvestigationPlan, attemptRoot, head, base, preparation, cycleID, findingDigest string, findingKeys []string, policyChecks []string) (*factory.InvestigationPlan, error) {
	if err := checkCorrectionPlanAdmission(attemptRoot, head, base, preparation, cycleID, findingDigest, acceptance, investigation, policyChecks); err != nil {
		return nil, err
	}
	ownership, err := deriveCorrectionOwnership(investigation)
	if err != nil {
		return nil, err
	}
	keys := dedupeCorrectionFindingKeys(findingKeys)
	checkIDs := make([]string, 0, len(investigation.Checks))
	checks := make([]factory.CheckSpec, 0, len(investigation.Checks))
	for _, check := range investigation.Checks {
		checks = append(checks, check)
		checkIDs = append(checkIDs, check.ID)
	}
	shortHead, shortBase := head, base
	if len(shortHead) > 8 {
		shortHead = shortHead[:8]
	}
	if len(shortBase) > 8 {
		shortBase = shortBase[:8]
	}
	idSum := sha256.Sum256([]byte("soda-factory-correction-plan-v1\x00" + attemptRoot + "\x00" + cycleID + "\x00" + head))
	inputSum := sha256.Sum256([]byte("soda-factory-correction-input-v1\x00" + acceptance.ID + "\x00" + head + "\x00" + cycleID + "\x00" + findingDigest))
	plan := &factory.InvestigationPlan{
		ID:                hex.EncodeToString(idSum[:])[:32],
		AttemptRoot:       attemptRoot,
		ScopeDigest:       investigation.ScopeDigest,
		InputDigest:       hex.EncodeToString(inputSum[:]),
		SourceCommit:      head,
		IntegrationCommit: base,
		Preparation:       preparation,
		Approach: "Apply the minimal tree changes addressing each carried finding's required outcome on head " +
			shortHead + " at base " + shortBase + "; run every staged check; keep the approved base and PR base unchanged.",
		AcceptanceCoverage: append([]string(nil), investigation.AcceptanceCoverage...),
		Inspected:          append([]factory.InspectedSource(nil), investigation.Inspected...),
		Findings:           keys,
		Tasks: []factory.PlanTask{{
			ID:             "correct",
			Objective:      "Address the review cycle findings on head " + shortHead + " per their required outcomes, verified by the staged checks.",
			Outputs:        []string{"corrected-tree"},
			AcceptanceRefs: append([]string(nil), investigation.AcceptanceCoverage...),
			Lane:           "correct",
			OwnedPaths:     ownership,
			CheckIDs:       checkIDs,
		}},
		Lanes:  []factory.PlanLane{{ID: "correct", TaskIDs: []string{"correct"}}},
		Checks: checks,
	}
	digest, err := factory.PlanDigest(*plan)
	if err != nil {
		return nil, err
	}
	plan.Digest = digest
	if err := plan.Validate(); err != nil {
		return nil, err
	}
	if err := plan.Executable(); err != nil {
		return nil, err
	}
	return plan, nil
}
