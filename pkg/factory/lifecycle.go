// Lifecycle intervention records: sticky pause/cancel, remaining-allowance
// resume/retry decisions and fixed takeover. These are pure domain records;
// the coordinator orders withdrawal before stopping, and later operation
// tasks connect native cancellation to the same withdrawal path.
package factory

import (
	"encoding/json"
	"errors"

	"github.com/levitateos/factory/pkg/factory/execution"
	"github.com/levitateos/factory/pkg/project"
)

// Repository control actions admitted on one repository's dispatch gate.
const (
	// ActionPause closes dispatch and stops outstanding runs. It stays
	// latched until an explicit resume reopens the gate.
	ActionPause = "pause"
	// ActionResume reopens a paused gate after every recorded run settles
	// and every grant revalidates. It launches nothing by itself.
	ActionResume = "resume"
)

// Run control actions admitted on one recorded run.
const (
	// ActionStop retires one run through the same stop path as the
	// operator command. A stopped run identity never runs again.
	ActionStop = "stop"
	// ActionRetry records a queued explicit retry after the prior run is
	// reconciled. No launcher consumes it yet; it stays queued.
	ActionRetry = "retry"
	// ActionDeliveryRepair requests a bounded repair of an exact, retired
	// application verification failure without repeating the verifier.
	ActionDeliveryRepair = "repair_delivery"
	// ActionTakeover copies a reconciled run's retained work into the
	// admitted member's own checkout. It never transfers credentials.
	ActionTakeover = "takeover"
)

// ValidRepositoryAction reports whether action is a repository control.
func ValidRepositoryAction(action string) bool {
	return action == ActionPause || action == ActionResume || action == ActionCompletionRetry || action == ActionInspectExternalPull || action == CommandAdoptExternalPull
}

// ValidRunAction reports whether action is a run control.
func ValidRunAction(action string) bool {
	return action == ActionStop || action == ActionRetry || action == ActionTakeover || action == ActionDeliveryRepair
}

// Withdrawal causes owned by lifecycle controls. Grant withdrawals keep
// their own causes; these name the control that closed dispatch.
const (
	CauseControlPaused = "control_paused"
	CauseProjectStop   = "project_stop"
)

// RunStopOutcome is one run's effect status inside a pause or project-stop
// receipt. The action (dispatch closed) and each native effect (run
// confirmed or still uncertain) stay separate facts.
type RunStopOutcome struct {
	ID        string `json:"id"`
	Outcome   string `json:"outcome,omitempty"`
	Reason    string `json:"reason,omitempty"`
	Confirmed bool   `json:"confirmed"`
	Uncertain bool   `json:"uncertain"`
}

// PauseResult reports the closed dispatch gate and each outstanding run's
// current stop outcome.
// Uncertain runs stay fenced; they never appear cancelled.
type PauseResult struct {
	Withdrawal Withdrawal       `json:"withdrawal"`
	Runs       []RunStopOutcome `json:"runs"`
	Paused     bool             `json:"paused"`
}

// ResumeResult reports the gate revision and current authority. Queued work
// stays queued; resume launches nothing.
type ResumeResult struct {
	Effective EffectiveAuthority `json:"effective"`
	Revision  int64              `json:"revision"`
	Reopened  bool               `json:"reopened"`
}

// RetryDecision projects one admitted retry request. The prior run
// is reconciled and current authority revalidated, but remaining allowances
// cannot be established without usage records, so the retry stays queued
// instead of launching. A later scheduler consumes it; nothing else may.
type RetryDecision struct {
	Prior     string `json:"prior_run"`
	CommandID string `json:"command_id"`
	Reason    string `json:"reason"`
	Queued    bool   `json:"queued"`
}

// RetryDecision reads the queue identity from its canonical request payload.
func (c Command) RetryDecision() (RetryDecision, error) {
	var request struct {
		Prior string `json:"prior_run"`
	}
	if err := json.Unmarshal([]byte(c.Payload), &request); err != nil {
		return RetryDecision{}, err
	}
	reason := "new attempt queued; dispatch will revalidate current policy and provider budget"
	if c.Type == CommandRetryImplementation {
		reason = "implementation retry queued with the accepted plan and remaining attempt allowance"
	}
	return RetryDecision{Prior: request.Prior, CommandID: c.ID, Reason: reason, Queued: true}, nil
}

func (d RetryDecision) Validate() error {
	if !ValidID(d.CommandID) || !ValidID(d.Prior) || !d.Queued {
		return errors.New("invalid retry decision")
	}
	if d.Reason == "" || len(d.Reason) > 256 {
		return errors.New("invalid retry queue reason")
	}
	return nil
}

// TakeoverRecord binds settled retained work to an admitted destination. An
// appliance member gets the host-derived checkout path; native recovery uses
// owned profile/checkout references. The runtime must exclude provider homes
// and role Git configuration from the copy.
type TakeoverRecord struct {
	Native  *execution.NativeRecovery `json:"native,omitempty"`
	Run     string                    `json:"run"`
	Member  string                    `json:"member,omitempty"`
	Project string                    `json:"project"`
	Dest    string                    `json:"destination,omitempty"`
	Copied  string                    `json:"copied"`
}

func (r TakeoverRecord) Validate() error {
	if !ValidID(r.Run) || !ValidProjectID(r.Project) {
		return errors.New("invalid takeover identity")
	}
	if r.Native != nil {
		request := execution.WorkRecoveryRequest{Address: execution.RunAddress{Run: r.Run, Workspace: r.Project}, Member: r.Member, Native: r.Native}
		return (execution.WorkRecoveryReceipt{Request: request, Destination: r.Dest}).ValidateFor(request)
	}
	if !project.ValidLogin(r.Member) || r.Member == "root" {
		return errors.New("invalid takeover member")
	}
	if r.Dest != project.TakeoverDestination(r.Member, r.Run) {
		return errors.New("invalid takeover destination")
	}
	return nil
}

func (r TakeoverRecord) TargetKey() string {
	return (execution.WorkRecoveryRequest{Member: r.Member, Native: r.Native}).Target().Key()
}

func (r TakeoverRecord) Equal(other TakeoverRecord) bool {
	a, b := r, other
	a.Native, b.Native = nil, nil
	return a == b && (r.Native == nil && other.Native == nil || r.Native != nil && other.Native != nil && *r.Native == *other.Native)
}

func (r TakeoverRecord) BindsRun(run Run) bool {
	if r.Run != run.ID || r.Project != run.ProjectID || !run.Reconciled {
		return false
	}
	if r.Native == nil {
		return run.Native == nil
	}
	if run.Native == nil || !run.Native.Settled() {
		return false
	}
	p := run.Native.Provenance
	return r.Native.Source == (execution.NativeWorkspaceRef{Profile: p.Profile, InstanceID: p.InstanceID, WorktreeID: p.WorktreeID})
}

// StartVerification reports one coordinated Project start: the host
// lifecycle result plus proof that no old run revived. Revived names runs
// with a live lease or live native boundary; unverified names runs whose
// state could not be established, including runs the host or broker never
// recorded. Leases stay closed, grants and readiness are untouched, and
// the maintenance hold keeps whatever state it had.
type StartVerification struct {
	Revived      []string `json:"revived"`
	Unverified   []string `json:"unverified"`
	Hold         bool     `json:"hold"`
	Started      bool     `json:"started"`
	DispatchOpen bool     `json:"dispatch_open"`
}

func (v StartVerification) Validate() error {
	if !v.Started {
		return errors.New("invalid start verification")
	}
	for _, id := range v.Revived {
		if !ValidID(id) {
			return errors.New("invalid revived run identity")
		}
	}
	for _, id := range v.Unverified {
		if !ValidID(id) {
			return errors.New("invalid unverified run identity")
		}
	}
	return nil
}
