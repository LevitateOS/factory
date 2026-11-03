package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/store"
)

// planPrereqBindingDomain versions the canonical prerequisite binding a
// reported investigation plan echoes and implementation admission rechecks.
// Collection order is not a binding; every digest covers one occurrence.
const planPrereqBindingDomain = "plan-prereq-v1"

// planPrerequisiteDigest binds one accepted prerequisite outcome reference:
// the edge, its outcome kind, and the endpoint acceptance (code head) or
// the satisfied native acceptance (result) the prompt carried.
func planPrerequisiteDigest(occurrence, dependsOn, outcome, ref string) string {
	sum := sha256.Sum256([]byte(planPrereqBindingDomain + "|" + occurrence + "|" + dependsOn + "|" + outcome + "|" + ref))
	return hex.EncodeToString(sum[:])
}

// livePlanBindings builds the current source, prerequisite and preparation
// values an implementation launch checks against the recorded head plan.
// A prerequisite without its current outcome reference leaves the bindings
// unbuildable: the caller waits for complete inputs instead of admitting.
func livePlanBindings(acceptance factory.Acceptance, control factory.IssueControl, tip, preparation string) (store.PlanBindings, error) {
	bindings := store.PlanBindings{SourceCommit: tip, Preparation: preparation}
	for _, prerequisite := range acceptance.Prerequisites {
		var ref string
		switch prerequisite.Outcome {
		case factory.PrereqCode:
			ref = control.EndpointHeads[prerequisite.Occurrence]
		case factory.PrereqResult:
			satisfied, ok := control.Satisfied[prerequisite.Occurrence]
			if !ok || satisfied.Validate() != nil || satisfied.Acceptance != control.Acceptance {
				return store.PlanBindings{}, errPlanInputsIncomplete
			}
			ref = satisfied.Acceptance
		default:
			return store.PlanBindings{}, errPlanInputsIncomplete
		}
		if ref == "" {
			return store.PlanBindings{}, errPlanInputsIncomplete
		}
		bindings.Prerequisites = append(bindings.Prerequisites,
			planPrerequisiteDigest(prerequisite.Occurrence, prerequisite.DependsOn, prerequisite.Outcome, ref))
	}
	sort.Strings(bindings.Prerequisites)
	if err := bindings.Validate(); err != nil {
		return store.PlanBindings{}, err
	}
	return bindings, nil
}

// errPlanInputsIncomplete reports prerequisite bindings the current inputs
// cannot supply. Admission waits; nothing stale is persisted or launched.
var errPlanInputsIncomplete = errors.New("plan prerequisite bindings are incomplete")

// admitInvestigationPlan persists the plan a completed investigation
// reported and admits the current head for implementation. The first persist
// records the immutable revision; an identical replay commits nothing and
// admits the same head, so crash/replay spends no duplicate investigation.
// A reused plan identity carrying different content fails without touching
// the original; a head that moved or bindings that changed refuses as
// stale instead of launching code. Executability itself is enforced by
// advanceChildID before admission; anything unexecutable fails here too.
func admitInvestigationPlan(ctx context.Context, s *store.Store, owner factory.Assignment, live store.PlanBindings) (*factory.InvestigationPlan, error) {
	reported := reportedInvestigationPlan(owner)
	if reported == nil {
		return nil, errPlanInputsIncomplete
	}
	if reported.AttemptRoot != owner.AttemptRoot {
		return nil, store.ErrStalePlanBindings
	}
	// Persist the immutable revision first: identical replays commit
	// nothing, while a reused identity carrying different content fails
	// without touching the original.
	if err := s.SaveInvestigationPlan(ctx, *reported); err != nil {
		return nil, err
	}
	// Move the head pointer to the reported revision. A replay whose head
	// already arrived stays put; a same-root re-investigation advances
	// from the previous head; a lost race refuses as stale.
	headID, _, headErr := s.PlanHead(ctx, owner.AttemptRoot)
	switch {
	case headErr == nil && headID == reported.ID:
	case headErr == nil:
		if err := s.AdvancePlanHead(ctx, owner.AttemptRoot, headID, reported.ID); err != nil {
			if errors.Is(err, store.ErrStaleRevision) {
				return nil, store.ErrStalePlanBindings
			}
			return nil, err
		}
	case errors.Is(headErr, store.ErrNotFound):
		if err := s.AdvancePlanHead(ctx, owner.AttemptRoot, "", reported.ID); err != nil {
			if errors.Is(err, store.ErrStaleRevision) {
				return nil, store.ErrStalePlanBindings
			}
			return nil, err
		}
	default:
		return nil, headErr
	}
	admitted, err := s.AdmitPlanHead(ctx, owner.AttemptRoot, live)
	if err != nil {
		return nil, err
	}
	if admitted.Digest != reported.Digest {
		return nil, store.ErrStalePlanBindings
	}
	return &admitted, nil
}

// reportedInvestigationPlan extracts the executable plan a finished
// investigation owner reported. Shape, executability and owner bindings
// are enforced by advanceChildID before admission; this only unwraps.
func reportedInvestigationPlan(owner factory.Assignment) *factory.InvestigationPlan {
	if owner.Result == nil || owner.Result.PhaseResult == nil || owner.Result.PhaseResult.Output == nil {
		return nil
	}
	return owner.Result.PhaseResult.Output.Plan
}
