package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/execution"
	"github.com/levitateos/factory/pkg/project"
	"github.com/levitateos/factory/pkg/store"
)

// RequestDeliveryRepair queues exactly one explicitly requested diagnosis of an
// authenticated failed verification. Incomplete reports and invented failed
// executor receipts cannot become a repair cause. The admitted actor is a
// canonical Factory key; the command principal retains its caller-owned meaning.
func (c *Coordinator) RequestDeliveryRepair(ctx context.Context, principal string, actor int64, runID string) (factory.DeliveryRepairResult, error) {
	var empty factory.DeliveryRepairResult
	if c == nil || c.Store == nil || !factory.ValidID(runID) {
		return empty, errors.New("delivery repair is unavailable")
	}
	if actor <= 0 || principal == "" || len(principal) > 128 {
		return empty, ErrIneffectiveAuthority
	}
	cause, err := c.deliveryRepairCause(ctx, runID)
	if err != nil {
		return empty, err
	}
	if _, err = c.Store.DeliveryRepairRecord(ctx, cause.Repository, runID); errors.Is(err, store.ErrNotFound) {
		available, e := c.Store.DeliveryRepairRequestAvailable(ctx, cause, time.Now())
		if e != nil {
			return empty, e
		}
		if !available {
			return empty, errors.New("delivery repair current funding or stable intent allowance is unavailable")
		}
	} else if err != nil {
		return empty, err
	}
	if err = c.ensureDeliveryRepairOrigin(ctx, cause, principal, actor); err != nil {
		return empty, err
	}
	record, _, err := c.Store.RecordDeliveryRepairCause(ctx, cause, time.Now())
	if err != nil {
		return empty, err
	}
	return factory.DeliveryRepairResult{RunID: runID, CauseID: factory.DeliveryRepairCauseID(runID), CycleID: record.Subject.CycleID, State: "queued"}, nil
}

func (c *Coordinator) saveDeliveryRepair(ctx context.Context, record store.DeliveryRepairRecord, state, reason string) {
	record.Cycle.State, record.Cycle.Reason = state, reason
	record.Revision++
	_ = c.Store.SaveDeliveryRepairRecord(ctx, record, record.Revision-1)
}

func (c *Coordinator) driveDeliveryRepairs(ctx context.Context) {
	if c.Store == nil || c.Host == nil || c.Broker == nil || !c.completionDriveDeps().available() {
		return
	}
	records, next, err := c.Store.PendingDeliveryRepairRecords(ctx, c.deliveryRepairCursor, 16)
	if err == nil && next == "" && c.deliveryRepairCursor != "" {
		records, next, err = c.Store.PendingDeliveryRepairRecords(ctx, "", 16)
	}
	if err != nil {
		return
	}
	c.deliveryRepairCursor = next
	for _, record := range records {
		if ctx.Err() != nil {
			return
		}
		switch record.Cycle.State {
		case factory.RepairCollecting:
			sealed := bracketMaterialCollect(ctx, c.completionDriveDeps(), record.Cause.Repository, 0, nil)
			if sealed.sealed == nil {
				c.saveDeliveryRepair(ctx, record, factory.RepairCollecting, sealed.hold)
				continue
			}
			s := sealed.sealed
			if s.Local.SourceCommit != record.Cause.Delivery.SourceCommit {
				c.saveDeliveryRepair(ctx, record, factory.RepairAttention, "delivery source changed before repair collection")
				continue
			}
			record.Subject.InventoryDigest = s.Digest
			record.Cycle.Local, record.Cycle.Issues, record.Cycle.InventoryDigest, record.Cycle.NativeRevision = s.Local, s.Bodies, s.Digest, s.Revision
			c.saveDeliveryRepair(ctx, record, factory.RepairReady, "")
		case factory.RepairReady:
			if err := c.dispatchDeliveryRepair(ctx, record); err != nil {
				c.saveDeliveryRepair(ctx, record, factory.RepairReady, "delivery repair is waiting for current authority, funding or preparation")
			}
		case factory.RepairInvestigating, factory.RepairAssessing:
			c.driveDeliveryRepairResult(ctx, record)
		case factory.RepairApplying:
			c.driveDeliveryRepairAdoption(ctx, record)
		}
	}
}

func (c *Coordinator) dispatchDeliveryRepair(ctx context.Context, record store.DeliveryRepairRecord) error {
	grant, err := c.Store.RepairGrantCommand(ctx, record.Cause.Repository)
	if err != nil {
		return err
	}
	charges, err := c.Store.RepairCharges(ctx, record.Cause.Repository)
	if err != nil {
		return err
	}
	minutes := grant.ActiveMinutes
	for _, prior := range charges {
		if prior.Kind == factory.RepairChargeRun {
			minutes -= prior.ActiveMinutes
		}
	}
	charge := factory.RepairCharge{ID: factory.RepairChargeID(factory.DeliveryRepairCauseID(record.Cause.FailedVerificationRunID), factory.RepairChargeRun, 1), GrantID: grant.ID, Kind: factory.RepairChargeRun, EpisodeID: factory.DeliveryRepairCauseID(record.Cause.FailedVerificationRunID), Cycle: 1, EvidenceDigest: record.Subject.InventoryDigest, ActiveMinutes: min(factory.DefaultRepairCycleActiveMinutes, minutes), SpentUnix: time.Now().Unix()}
	originMinutes, err := c.Store.RemainingDeliveryRepairMinutes(ctx, record.Cause.Repository, record.Cause.FailedVerificationRunID, time.Now())
	if err != nil {
		return err
	}
	charge.ActiveMinutes = min(charge.ActiveMinutes, originMinutes)
	for _, prior := range charges {
		if prior.ID == charge.ID {
			charge = prior
			break
		}
	}
	if charge.ActiveMinutes < 1 {
		return factory.ErrRepairTimeExhausted
	}
	if _, err = c.Store.AdmitDeliveryRepairInvestigation(ctx, record.Cause.Repository, record.Cause.FailedVerificationRunID, charge, time.Now()); err != nil {
		return err
	}
	preparations, err := c.Store.ProjectPreparations(ctx, record.Cause.ProjectID)
	if err != nil {
		return err
	}
	prep := ""
	for _, p := range preparations {
		if p.Preparation.Role == project.RoleCoder && p.State.Ready && !p.State.Stopped && p.Preparation.SourceCommit == record.Cycle.Local.SourceCommit {
			prep = p.Preparation.ID
			break
		}
	}
	if prep == "" {
		return errors.New("delivery repair needs ready current source preparation")
	}
	input := factory.CurrentDeliveryRepairContext{Subject: record.Subject, Cause: record.Cause, Local: record.Cycle.Local, Issues: record.Cycle.Issues, Complete: true, State: record.Cycle.State}
	raw, err := json.Marshal(input)
	if err != nil {
		return err
	}
	prompt := append([]byte("You are Strategist for this explicitly requested failed-application repair. Inspect factory.context and the exact failed delivery/outcomes. Propose only idle in-scope follow-up work using the accepted intent, preserve all delivered/manual/active/PR-bound work and origin funding. Do not change code or claim verification. A material product decision or missing authorization needs owner attention. Finish factory.finish_reassessment with DeliveryRepairReport bound to this subject; causes must name the recorded failed outcomes. No-change never verifies the failed app.\n\n"+reassessmentWorkGuidance+"\n\n"), raw...)
	occupancy, err := snapshotOccupancy(ctx, c.Store)
	if err != nil {
		return err
	}
	report := newDispatchReport()
	DispatchTimeOnlyRoot(ctx, c.dispatchDeps(), &occupancy, record.Cause.Repository, factory.ExecutionSubject{Kind: factory.SubjectDeliveryRepair, DeliveryRepair: &record.Subject}, record.Cycle.Local.SourceCommit, prompt, prep, &report)
	if len(report.Errors) > 0 {
		return errors.New(report.Errors[0].Detail)
	}
	if len(report.Waits) > 0 {
		return errors.New(report.Waits[0].Reason)
	}
	return nil
}

func (c *Coordinator) deliveryRepairCause(ctx context.Context, runID string) (factory.DeliveryRepairCause, error) {
	var none factory.DeliveryRepairCause
	if c.Host == nil {
		return none, errors.New("delivery repair native evidence is unavailable")
	}
	a, err := c.Store.AssignmentByRun(ctx, runID)
	if err != nil {
		return none, err
	}
	run, err := c.Store.FactoryRun(ctx, runID)
	if err != nil {
		return none, err
	}
	if a.Stage != factory.AssignmentFinished || a.Phase != project.ExecutionVerify || a.Subject.Delivery == nil || a.Outcome != factory.Succeeded || a.Result == nil || !a.Result.Reported || a.Result.PhaseResult == nil || a.Result.PhaseResult.Output == nil || a.Result.PhaseResult.Output.Verification == nil || a.Result.PhaseResult.BindAssignment(a) != nil || !run.Reconciled || run.Outcome != factory.Succeeded || !run.CredentialsReturned() {
		return none, errors.New("delivery repair requires a settled failed verification")
	}
	effective, err := c.EffectiveAuthority(ctx, a.Repository)
	if err != nil {
		return none, err
	}
	for _, missing := range effective.Missing {
		if missing != "policy_paused" && missing != "dispatch_closed" {
			return none, ErrIneffectiveAuthority
		}
	}
	state, err := c.Host.InspectRun(ctx, execution.RunAddress{Workspace: a.ProjectID, Run: runID})
	if err != nil {
		return none, err
	}
	if state.Run != runID || state.Workspace != a.ProjectID || state.Role != a.Role || state.Live || state.Retirement != execution.Retired || !state.CredentialReturnObserved() {
		return none, errors.New("failed verification has not retired")
	}
	report := *a.Result.PhaseResult.Output.Verification
	if report.Validate() != nil || report.Outcome != factory.AppVerificationFailed {
		return none, errors.New("only a failed verification can request delivery repair")
	}
	capability, err := c.Store.GetMCPCapability(ctx, runID)
	if err != nil {
		return none, err
	}
	if capability.AssignmentID != a.ID || capability.RunID != runID || capability.ScopeDigest() != report.InputDigest {
		return none, errors.New("failed verification differs from its admitted input scope")
	}
	evidence, err := c.ApplicationVerificationEvidence(ctx, a)
	if err != nil {
		return none, err
	}
	receipt := evidence.Receipt
	if evidence.ExecutionRequired {
		if evidence.ExecutionReceipt == nil {
			return none, errors.New("failed verification lacks its native manifest execution receipt")
		}
		receipt = *evidence.ExecutionReceipt
	}
	if len(report.CheckReceipts) != 1 || report.CheckReceipts[0].CheckID != receipt.CheckID || report.CheckReceipts[0].Outcome != receipt.Outcome || report.CheckReceipts[0].ArtifactDigest != receipt.ArtifactDigest {
		return none, errors.New("failed report differs from trusted native verification evidence")
	}
	outcomes := map[string]bool{}
	for _, id := range evidence.Manifest.Outcomes {
		outcomes[id] = true
	}
	for _, failure := range report.Failures {
		if !outcomes[failure.CheckRef] {
			return none, errors.New("delivery failure must name an exact accepted outcome")
		}
	}
	cause := factory.DeliveryRepairCause{Repository: a.Repository, ProjectID: a.ProjectID, FailedAssignmentID: a.ID, FailedVerificationRunID: runID, Delivery: *a.Subject.Delivery, Report: report, FailureDigest: factory.DeliveryFailureDigest(report)}
	if cause.Validate() != nil {
		return none, errors.New("invalid delivery repair cause")
	}

	return cause, nil
}

// DeliveryRepairAvailable is a spend-free native/Store read. Every mutation
// repeats these facts; an incomplete or stale report never offers repair.
func (c *Coordinator) DeliveryRepairAvailable(ctx context.Context, runID string) (bool, error) {
	if c == nil || c.Store == nil || !factory.ValidID(runID) {
		return false, nil
	}
	a, err := c.Store.AssignmentByRun(ctx, runID)
	if err != nil {
		return false, err
	}
	if a.Stage != factory.AssignmentFinished || a.Phase != project.ExecutionVerify || a.Subject.Delivery == nil || a.Result == nil || a.Result.PhaseResult == nil || a.Result.PhaseResult.Output == nil || a.Result.PhaseResult.Output.Verification == nil || a.Result.PhaseResult.Output.Verification.Outcome != factory.AppVerificationFailed {
		return false, nil
	}
	cause, err := c.deliveryRepairCause(ctx, runID)
	if err != nil {
		return false, nil
	}
	return c.Store.DeliveryRepairRequestAvailable(ctx, cause, time.Now())
}

// Only this explicit intent owner's action admits the canonical default when
// no origin limit exists. Existing inactive, consumed or tighter limits remain
// authoritative; actual consumption continues to come from retained roots.
func (c *Coordinator) ensureDeliveryRepairOrigin(ctx context.Context, cause factory.DeliveryRepairCause, principal string, owner int64) error {
	revision, err := c.Store.IntentRevision(ctx, cause.Delivery.IntentID, owner)
	if err != nil {
		return err
	}
	if revision.Repository != cause.Repository || revision.ID != cause.Delivery.AcceptedRevision || revision.Digest() != cause.Delivery.ScopeDigest {
		return store.ErrStaleRevision
	}
	limit, err := c.Store.IntentExecutionLimit(ctx, revision.Intent)
	if err == nil {
		if !limit.Active || limit.OwnerID != owner || limit.RepositoryID != cause.Repository {
			return ErrIneffectiveAuthority
		}
		return nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	limit = factory.DefaultIntentExecutionAllowance()
	limit.IntentID, limit.OwnerID, limit.RepositoryID, limit.Active, limit.CheckpointUnix = revision.Intent, owner, cause.Repository, true, time.Now().Unix()
	raw, _ := json.Marshal(limit)
	sum := sha256.Sum256([]byte("owner-default-intent-execution\x00" + revision.Intent))
	cmd := factory.Command{ID: hex.EncodeToString(sum[:16]), Type: factory.CommandIntentExecutionLimit, Target: factory.IntentExecutionTarget(revision.Intent), Principal: principal, Payload: string(raw)}
	cmd.Digest = factory.SettingsDigest(cmd.Type, cmd.Target, cmd.Payload)
	_, _, err = c.Store.ApplyIntentExecutionLimitCommand(ctx, cmd, limit, time.Now())
	return err
}
