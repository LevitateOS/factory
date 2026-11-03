package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/store"
)

type contextSelectionCurrent struct {
	Required       *contextRequiredCoverage `json:"required_coverage,omitempty"`
	SemanticDigest string                   `json:"semantic_digest,omitempty"`
	Availability   string                   `json:"availability"`
	MissingReasons []string                 `json:"missing_reasons"`
	RequiredDigest string                   `json:"required_digest"`
	RequiredBytes  int                      `json:"required_bytes"`
	Tasks          int                      `json:"tasks"`
	Findings       int                      `json:"findings"`
	Stage          string                   `json:"stage"`
	Active         *bool                    `json:"active"`
	Settled        *bool                    `json:"settled"`
}

// These are facts about the admitted projection, not its private prose or a
// model judgment about usefulness. They make bundle differences observable.
type contextRequiredCoverage struct {
	Fields     []string                 `json:"fields"`
	Acceptance *contextAcceptedCoverage `json:"acceptance,omitempty"`
	Plan       *contextPlanCoverage     `json:"plan,omitempty"`
}

type contextAcceptedCoverage struct {
	ID                string `json:"id"`
	SourceCommit      string `json:"source_commit"`
	ObjectivePresent  bool   `json:"objective_present"`
	RepositoryContext bool   `json:"repository_context"`
	Sources           int    `json:"sources"`
	Resolutions       int    `json:"resolutions"`
	Prerequisites     int    `json:"prerequisites"`
	RequiredChecks    int    `json:"required_checks"`
}

type contextPlanCoverage struct {
	ID             string                   `json:"id"`
	Digest         string                   `json:"digest"`
	AttemptRoot    string                   `json:"attempt_root"`
	SourceCommit   string                   `json:"source_commit"`
	Generation     int                      `json:"generation"`
	CurrentHead    *factory.CurrentPlanHead `json:"current_head"`
	Tasks          int                      `json:"tasks"`
	Checks         int                      `json:"checks"`
	TaskReceipts   int                      `json:"task_receipts"`
	AcceptanceRefs int                      `json:"task_acceptance_refs"`
	OwnedPaths     int                      `json:"task_owned_paths"`
}

type contextHistoryPreview struct {
	Profile       factory.MCPProfile `json:"profile,omitempty"`
	Additional    []string           `json:"additional_fields,omitempty"`
	PlanInCurrent *bool              `json:"result_plan_in_required_current,omitempty"`
	Relation      string             `json:"relation"`
	ID            string             `json:"id"`
	Phase         string             `json:"phase,omitempty"`
	Outcome       factory.Outcome    `json:"outcome,omitempty"`
	Reason        string             `json:"reason,omitempty"`
	Reconciled    *bool              `json:"reconciled,omitempty"`
}

type contextBundleDescription struct {
	AdditionalFields []string `json:"additional_fields,omitempty"`
	Eligible         bool     `json:"eligible"`
	RequiredStreams  []string `json:"required_streams"`
	HistoryRecords   int      `json:"history_records"`
}

type currentVerificationSource func(context.Context, factory.Assignment) (factory.CurrentVerificationEvidence, error)

func loadContextBundleState(ctx context.Context, st *store.Store, cap factory.MCPCapability, a factory.Assignment, verify currentVerificationSource) (contextBundleState, bool, bool, error) {
	state := contextBundleStateFor(cap, a)
	current, err := st.FactoryCurrentContext(ctx, cap, time.Now())
	if err != nil {
		return state, false, false, err
	}
	if cap.Profile == factory.MCPProfileTestPilot && verify != nil && current.Role.Delivery != nil {
		evidence, err := verify(ctx, a)
		if err == nil && evidence.ValidateFor(*current.Role.Delivery) == nil {
			current.Role.Verification = &evidence
			missing := []string{}
			for _, reason := range current.Evidence.MissingReasons {
				if reason != "current_native_verification_unavailable" {
					missing = append(missing, reason)
				}
			}
			current.Evidence.MissingReasons = missing
			if len(missing) == 0 {
				current.Evidence.Availability = "complete"
			}
		}
	}
	raw, err := json.Marshal(current.Role)
	if err != nil {
		return state, false, false, err
	}
	if len(raw) > 1024*1024 {
		return state, false, false, errors.New("required current context exceeds its bound")
	}
	digest := sha256.Sum256(raw)
	state.Current = contextSelectionCurrent{Availability: current.Evidence.Availability, MissingReasons: current.Evidence.MissingReasons, RequiredDigest: hex.EncodeToString(digest[:]), RequiredBytes: len(raw), Stage: a.Stage}
	state.Current.SemanticDigest, err = factory.CurrentRequiredSemanticDigest(current.Role)
	if err != nil {
		return state, false, false, err
	}
	coverage := &contextRequiredCoverage{Fields: []string{}}
	var roleFields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &roleFields); err != nil {
		return state, false, false, err
	}
	for field := range roleFields {
		coverage.Fields = append(coverage.Fields, field)
	}
	sort.Strings(coverage.Fields)
	if accepted := current.Role.Accepted; accepted != nil {
		coverage.Acceptance = &contextAcceptedCoverage{ID: accepted.AcceptanceID, SourceCommit: accepted.SourceCommit, ObjectivePresent: accepted.Title != "", RepositoryContext: accepted.RepositoryContext != nil, Sources: len(accepted.Sources), Resolutions: len(accepted.Resolutions), Prerequisites: len(accepted.Prerequisites), RequiredChecks: len(accepted.RequiredChecks)}
	}
	if current.Role.Plan != nil {
		plan := current.Role.Plan
		state.Current.Tasks = len(plan.Tasks)
		coverage.Plan = &contextPlanCoverage{ID: plan.PlanID, Digest: plan.Digest, AttemptRoot: plan.AttemptRoot, SourceCommit: plan.SourceCommit, Generation: plan.Generation, CurrentHead: plan.CurrentPlanHead, Tasks: len(plan.Tasks), Checks: len(plan.Checks), TaskReceipts: len(plan.Receipts)}
		for _, task := range plan.Tasks {
			coverage.Plan.AcceptanceRefs += len(task.AcceptanceRefs)
			coverage.Plan.OwnedPaths += len(task.OwnedPaths)
		}
	}
	state.Current.Required = coverage
	if current.Role.Review != nil {
		state.Current.Findings = len(current.Role.Review.CarriedFindings)
	}
	if current.Run != nil {
		state.Current.Active, state.Current.Settled = &current.Run.Active, &current.Run.Settled
	}
	attempt := false
	for _, id := range a.RunHistory {
		if len(state.HistoryPreview) >= 16 {
			break
		}
		view, err := st.FactoryRunView(ctx, id)
		if err != nil || view.Attempt != a.ID || view.Repository != a.Repository {
			continue
		}
		run, err := st.FactoryRun(ctx, id)
		if err != nil || run.ID != id || run.ProjectID != a.ProjectID {
			continue
		}
		// Packet staging records RunHistory and Started before any native
		// launch. Neither that bookkeeping nor a confirmed-unused terminal
		// run provides execution continuity. Require a durable native
		// observation (retained after capability revocation), delegation or
		// checkpoint before offering the attempt bundle.
		liveObserved, observedErr := st.MCPRunWasObserved(ctx, run.ID, a.ID, a.ProjectID)
		if observedErr != nil {
			return state, false, false, observedErr
		}
		if !liveObserved && run.InvocationID() == "" && !(run.CredentialsDelegated() && run.IdentityBinding != nil) && run.FinalCheckpoint == nil {
			continue
		}
		attempt = true
		state.HistoryPreview = append(state.HistoryPreview, contextHistoryPreview{Relation: "own_assignment", ID: run.ID, Phase: a.Phase, Outcome: run.Outcome, Reconciled: &run.Reconciled})
	}
	lineage := false
	if a.ParentAssignment != "" {
		parent, err := st.Assignment(ctx, a.ParentAssignment)
		if err == nil && factory.ContextParentVisible(a, parent) {
			lineage = true
			profile, _ := mcpProfileForPhase(parent.Phase)
			preview := contextHistoryPreview{Relation: "parent", ID: parent.ID, Phase: parent.Phase, Profile: profile, Outcome: parent.Outcome, Reason: parent.Reason, Additional: []string{"lineage.parent_assignment"}}
			if parent.Outcome != "" {
				preview.Additional = append(preview.Additional, "lineage.parent_outcome")
			}
			if parent.Reason != "" {
				preview.Additional = append(preview.Additional, "lineage.parent_reason")
			}
			if plan := current.Role.Plan; plan != nil && parent.Result != nil && parent.Result.PhaseResult != nil && parent.Result.PhaseResult.Output != nil && parent.Result.PhaseResult.Output.Plan != nil {
				produced := parent.Result.PhaseResult.Output.Plan
				if produced.Validate() == nil {
					matches := produced.ID == plan.PlanID && produced.Digest == plan.Digest && produced.AttemptRoot == plan.AttemptRoot && produced.SourceCommit == plan.SourceCommit && produced.Preparation == plan.Preparation && produced.InputDigest == plan.InputDigest && produced.ScopeDigest == plan.ScopeDigest
					preview.PlanInCurrent = &matches
				}
			}
			state.HistoryPreview = append(state.HistoryPreview, preview)
		}
	}
	state.Bundles = describeContextBundles(attempt, lineage, len(state.HistoryPreview))
	// The descriptions name exactly the optional fields this prepared point
	// can add. Parent-only history must not masquerade as own-attempt history.
	own := 0
	lineageFields := []string(nil)
	if attempt {
		lineageFields = append(lineageFields, "attempt.run_history")
	}
	for _, preview := range state.HistoryPreview {
		if preview.Relation == "own_assignment" {
			own++
		} else if preview.Relation == "parent" {
			lineageFields = append(lineageFields, preview.Additional...)
		}
	}
	ownBundle := state.Bundles[factory.ContextBundleAttempt]
	ownBundle.HistoryRecords = own
	state.Bundles[factory.ContextBundleAttempt] = ownBundle
	lineageBundle := state.Bundles[factory.ContextBundleLineage]
	lineageBundle.AdditionalFields = lineageFields
	state.Bundles[factory.ContextBundleLineage] = lineageBundle
	return state, attempt, lineage, nil
}

func describeContextBundles(attempt, lineage bool, history int) map[string]contextBundleDescription {
	attemptFields := []string(nil)
	if attempt {
		attemptFields = []string{"attempt.run_history"}
	}
	lineageFields := append([]string(nil), attemptFields...)
	if lineage {
		lineageFields = append(lineageFields, "lineage.parent_assignment", "lineage.parent_outcome", "lineage.parent_reason")
	}
	return map[string]contextBundleDescription{
		factory.ContextBundleCurrent:      {Eligible: true, RequiredStreams: []string{"required_current"}},
		factory.ContextBundleAttempt:      {Eligible: attempt, RequiredStreams: []string{"required_current"}, HistoryRecords: history, AdditionalFields: attemptFields},
		factory.ContextBundleLineage:      {Eligible: lineage, RequiredStreams: []string{"required_current"}, HistoryRecords: history, AdditionalFields: lineageFields},
		factory.ContextBundleSnapshotHold: {Eligible: true, RequiredStreams: []string{"required_current"}},
	}
}
