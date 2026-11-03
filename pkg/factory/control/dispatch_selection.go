package control

import (
	"context"
	"errors"
	"time"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/execution"
	"github.com/levitateos/factory/pkg/project"
	"github.com/levitateos/factory/pkg/store"
)

// selectSponsorship picks the authority-bound active sponsorship: the
// first active grant in connection order, at its bound revision, with
// permission for the requested dispatch role. Any other connection is
// never a fallback.
func selectSponsorship(ctx context.Context, deps DispatchDeps, repository int64, effective factory.EffectiveAuthority, role string) (factory.Sponsorship, *planWait, *planWait) {
	sponsorships, err := deps.Store.Sponsorships(ctx, repository)
	if err != nil {
		return factory.Sponsorship{}, nil, waitFor(DispatchErrStore, "sponsorships unreadable")
	}
	for _, sponsorship := range sponsorships {
		if !sponsorship.Active {
			continue
		}
		if sponsorship.Validate() != nil {
			return factory.Sponsorship{}, waitFor(WaitAuthority, "sponsorship execution identity is unavailable"), nil
		}
		if sponsorship.Revision != effective.Authority.Sponsorship || sponsorship.Connection != effective.Authority.SponsorshipConnection {
			return factory.Sponsorship{}, waitFor(WaitAuthority, "sponsorship changed during dispatch"), nil
		}
		permitted := false
		for _, grantedRole := range sponsorship.Roles {
			permitted = permitted || grantedRole == role
		}
		if !permitted {
			return factory.Sponsorship{}, waitFor(WaitSponsorshipRole, "sponsorship does not permit this role"), nil
		}
		return sponsorship, nil, nil
	}
	return factory.Sponsorship{}, waitFor(WaitAuthority, "sponsorship changed during dispatch"), nil
}

func checkConcurrencyLimits(ctx context.Context, deps DispatchDeps, occupancy *passOccupancy, plan *attemptPlan) (*planWait, *planWait) {
	capacity, err := deps.Store.Capacity(ctx)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return waitFor(WaitAuthority, "capacity changed during dispatch"), nil
		}
		return nil, waitFor(DispatchErrStore, "capacity unreadable")
	}
	if _, err := deps.Store.OperatorGrant(ctx, plan.policy.Repository); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return waitFor(WaitAuthority, "operator grant changed during dispatch"), nil
		}
		return nil, waitFor(DispatchErrStore, "operator grant unreadable")
	}
	repository := plan.policy.Repository
	if occupancy.heldTotal()+occupancy.unattributed >= capacity.MaxConcurrentRuns {
		return waitFor(WaitCapacity, "appliance runs at its limit"), nil
	}
	unsettled, err := deps.Store.ProjectUnsettledRuns(ctx, plan.projectID, 1)
	if err != nil {
		return nil, waitFor(DispatchErrStore, "repository run state unreadable")
	}
	if len(unsettled) != 0 {
		return waitFor(WaitRepository, "repository has an unsettled factory session"), nil
	}
	// One session per repository is a product invariant.
	if occupancy.heldRepo(repository)+occupancy.byProject[plan.projectID] >= 1 {
		return waitFor(WaitRepository, "repository runs at its limit"), nil
	}
	return nil, nil
}

// selectPreparationFor binds a ready role preparation built from the current
// requirement and approval decisions. An exact ID preserves child work.
func selectPreparationFor(ctx context.Context, deps DispatchDeps, plan *attemptPlan, role, exactID string) (*planWait, *planWait) {
	requirement, err := deps.Store.RequirementHead(ctx, plan.projectID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return waitFor(WaitPreparation, "project has no accepted requirements"), nil
		}
		return nil, waitFor(DispatchErrStore, "requirement head unreadable")
	}
	approval, err := deps.Store.ApprovalHead(ctx, plan.projectID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return waitFor(WaitPreparation, "project has no approved setup"), nil
		}
		return nil, waitFor(DispatchErrStore, "approval head unreadable")
	}
	var preparations []execution.PreparationRecord
	if exactID == "" {
		preparations, err = deps.Store.StandingProjectPreparations(ctx, plan.projectID)
	} else {
		preparations, err = deps.Store.ProjectPreparations(ctx, plan.projectID)
	}
	if err != nil {
		return nil, waitFor(DispatchErrStore, "preparations unreadable")
	}
	for _, prep := range preparations {
		// This dispatcher still constructs an appliance execution packet.
		// Native preparation ownership must survive storage without becoming
		// permission to execute it through that appliance path.
		if prep.Native != nil {
			continue
		}
		if prep.Preparation.Role != role || !prep.State.Ready || (exactID != "" && prep.Preparation.ID != exactID) {
			continue
		}
		if prep.Preparation.Requirements.ID != requirement || prep.Preparation.Approval.ID != approval {
			continue
		}
		plan.prep = prep
		plan.requirement, plan.approval = requirement, approval
		return nil, nil
	}
	return waitFor(WaitPreparation, "no ready role preparation matches the current decisions"), nil
}

// checkHarnessFor verifies the selected role's tool and runtime kind. Current
// appliance admission captures the proved bundle without changing retained pins.
func checkHarnessFor(ctx context.Context, deps DispatchDeps, plan *attemptPlan, role string) *planWait {
	selection := plan.policy.Roles[role]
	if selection.Harness == "" || selection.Model == "" {
		return waitFor(WaitHarness, "policy has no selection for this role")
	}
	tool, err := deps.Host.InspectTool(ctx, selection.Harness)
	if err != nil {
		return waitFor(WaitHarness, "execution tool identity unreadable")
	}
	if err := tool.ValidateFor(selection.Harness); err != nil {
		return waitFor(WaitHarness, "execution tool identity is invalid")
	}
	if tool.Appliance == nil {
		// This path admits an appliance preparation/environment. Native
		// placement/admission is a separate gate, never a fabricated image.
		return waitFor(WaitHarness, "native tool requires native execution admission")
	}
	pin := tool.Appliance
	// Retained appliance packets keep their exact existing pin shape.
	plan.pin = project.FactoryHarnessPin{Harness: pin.Harness, Version: pin.Version, SHA256: pin.SHA256, Image: pin.Image}
	return nil
}

// checkLimits enforces appliance, repository-session and sponsorship limits,
// then gives the run the one per-run limit. Leftover budgets never shorten a
// run: a nearly spent allowance still admits a full run.
func checkLimits(ctx context.Context, deps DispatchDeps, occupancy *passOccupancy, plan *attemptPlan) (*planWait, *planWait) {
	if wait, failed := checkConcurrencyLimits(ctx, deps, occupancy, plan); wait != nil || failed != nil {
		return wait, failed
	}
	if wait, failed := checkSponsorshipBudget(ctx, deps, plan); wait != nil || failed != nil {
		return wait, failed
	}
	return sizeAttemptDeadline(ctx, deps, plan)
}

// checkSponsorshipBudget is the one spend cap, checked softly at admission:
// any minutes left admit one full run, which may overrun the cap by that run.
func checkSponsorshipBudget(ctx context.Context, deps DispatchDeps, plan *attemptPlan) (*planWait, *planWait) {
	used, err := deps.Store.UsageTotal(ctx, plan.policy.Repository, plan.sponsorship.Connection)
	if err != nil {
		return nil, waitFor(DispatchErrStore, "confirmed usage unreadable")
	}
	if plan.sponsorship.AllowanceMinutes-used < 1 {
		return waitFor(WaitAllowance, "sponsorship allowance is exhausted"), nil
	}
	return nil, nil
}

// sizeAttemptDeadline gives the run the attempt's per-run limit, bounded by
// the host's supervised maximum.
func sizeAttemptDeadline(ctx context.Context, deps DispatchDeps, plan *attemptPlan) (*planWait, *planWait) {
	now := time.Now()
	plan.attemptLimits = plan.policy.AttemptLimits
	runSeconds := int64(plan.policy.AttemptLimits.ActiveMinutes) * 60
	if !plan.freshAttempt {
		allowance, allowanceErr := deps.Store.AttemptAllowance(ctx, plan.policy.Repository, plan.control.Issue)
		switch {
		case allowanceErr == nil:
			if allowance.Closed {
				return waitFor(WaitAttemptRecorded, "attempt is terminal; explicit Retry is required"), nil
			}
			plan.attemptRoot = allowance.RootAssignment
			plan.attemptLimits = allowance.Limits
			runSeconds = allowance.RemainingSeconds(now)
		case errors.Is(allowanceErr, store.ErrNotFound):
		default:
			return nil, waitFor(DispatchErrStore, "attempt allowance unreadable")
		}
	}
	if runSeconds <= 0 {
		return waitFor(WaitAllowance, "attempt has no run time"), nil
	}
	minutes := min(int(runSeconds/60), MaxDispatchDeadline)
	plan.planned = minutes
	plan.deadline = time.Unix(now.Unix()+int64(minutes)*60, 0)
	return nil, nil
}
