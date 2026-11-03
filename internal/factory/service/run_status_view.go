package service

import (
	"errors"
	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/factory/control"
	"github.com/levitateos/sodaos/internal/factory/execution"
	"github.com/levitateos/sodaos/internal/project"
	"github.com/levitateos/sodaos/internal/store"
	"math"
	"strconv"
	"time"
)

// factoryRunOutputExcerpt bounds the final-output excerpt carried by the run
// status read. Live bytes stream through the output route; an ended run
// exposes its outcome, never a replayable transcript.
const factoryRunOutputExcerpt = 16 * 1024

// factoryRunRecord carries the recorded run facts a viewer may see. Broker
// lease internals stay out; live process identity comes from host state.
type factoryRunRecord struct {
	Started    time.Time       `json:"started"`
	Deadline   time.Time       `json:"deadline"`
	Outcome    factory.Outcome `json:"outcome,omitempty"`
	ID         string          `json:"id"`
	ProjectID  string          `json:"project_id"`
	Role       string          `json:"role"`
	Harness    string          `json:"harness"`
	Model      string          `json:"model"`
	InputSHA   string          `json:"input_sha"`
	Summary    string          `json:"summary,omitempty"`
	Reconciled bool            `json:"reconciled"`
}

func factoryRunRecordDTO(r factory.Run) factoryRunRecord {
	return factoryRunRecord{
		Started: r.Started, Deadline: r.Deadline, Outcome: r.Outcome,
		ID: r.ID, ProjectID: r.ProjectID, Role: r.Role, Harness: r.Harness,
		Model: r.Model, InputSHA: r.InputSHA, Summary: r.Summary, Reconciled: r.Reconciled,
	}
}

// factoryRunBindingDTO renders the display binding with canonical IDs in
// decimal-string form. An unbound run renders no binding, never a guess.
type factoryRunBinding struct {
	Repository string `json:"repository"`
	Issue      string `json:"issue,omitempty"`
	Attempt    string `json:"attempt,omitempty"`
}

func factoryRunBindingDTO(v factory.RunView) *factoryRunBinding {
	binding := &factoryRunBinding{Repository: strconv.FormatInt(v.Repository, 10)}
	if v.Issue > 0 {
		binding.Issue = strconv.FormatInt(v.Issue, 10)
	}
	if v.Attempt != "" {
		binding.Attempt = v.Attempt
	}
	return binding
}

// factoryRunLive carries the observed host state for one run: its phase and
// exact process binding plus a bounded final-output excerpt.
type factoryRunLive struct {
	ExitCode        *int   `json:"exit_code,omitempty"`
	Live            bool   `json:"live"`
	Terminal        bool   `json:"terminal"`
	OutputTruncated bool   `json:"output_truncated"`
	Phase           string `json:"phase"`
	Container       string `json:"container,omitempty"`
	Unit            string `json:"unit,omitempty"`
	Invocation      string `json:"invocation,omitempty"`
	Reason          string `json:"reason,omitempty"`
	Retirement      string `json:"retirement,omitempty"`
	Output          string `json:"output,omitempty"`
}

// factoryHostTerminal reports whether a host phase is settled. Uncertain
// runs stay open: retirement or credential return is still unconfirmed.
func factoryHostTerminal(phase string) bool {
	switch phase {
	case project.FactoryCompleted, project.FactoryFailed, project.FactoryStopped:
		return true
	default:
		return false
	}
}

func factoryRunLiveDTO(out execution.RunObservation) *factoryRunLive {
	live := &factoryRunLive{ExitCode: out.ExitCode, Live: out.Live, Terminal: factoryHostTerminal(out.Phase),
		Phase: out.Phase, Invocation: out.InvocationID(), Reason: out.Reason, Retirement: string(out.Retirement)}
	if out.Retirement == execution.Retired {
		live.Retirement = "confirmed"
	}
	if out.Appliance != nil {
		live.Container, live.Unit = out.Appliance.Invocation.Container, out.Appliance.Invocation.Unit
	}
	if len(out.Output) > factoryRunOutputExcerpt {
		live.Output, live.OutputTruncated = out.Output[:factoryRunOutputExcerpt], true
	} else {
		live.Output = out.Output
	}
	return live
}

type RunStatusView struct {
	Run               factoryRunRecord              `json:"run"`
	View              *factoryRunBinding            `json:"view,omitempty"`
	State             *factoryRunLive               `json:"state,omitempty"`
	ContextSelection  *control.ContextSelectionView `json:"context_selection,omitempty"`
	VerificationRetry *WorkCapability               `json:"verification_retry,omitempty"`
	DeliveryRepair    *factoryDeliveryRepairView    `json:"delivery_repair,omitempty"`
}

type factoryDeliveryRepairView struct {
	Available bool                           `json:"available"`
	Progress  *factoryDeliveryRepairProgress `json:"progress,omitempty"`
}

// Only bounded lifecycle/cause facts leave the private repair journal. Its
// collector inputs, native source material and adoption packets stay private.
type factoryDeliveryRepairProgress struct {
	CauseID      string                         `json:"cause_id"`
	CycleID      string                         `json:"cycle_id"`
	FailedRun    string                         `json:"failed_run"`
	SourceCommit string                         `json:"source_commit"`
	State        string                         `json:"state"`
	Reason       string                         `json:"reason,omitempty"`
	RunID        string                         `json:"run_id,omitempty"`
	Failures     []factory.CheckFailure         `json:"failures"`
	FailureCount int                            `json:"failure_count"`
	Judgment     *factoryDeliveryRepairJudgment `json:"judgment,omitempty"`
}

type factoryDeliveryRepairJudgment struct {
	Option      string  `json:"option"`
	Probability float64 `json:"accept_probability"`
	Confidence  float64 `json:"confidence"`
}

func deliveryRepairProgressDTO(record store.DeliveryRepairRecord, assignment factory.Assignment) (*factoryDeliveryRepairProgress, error) {
	cause, cycle := record.Cause, record.Cycle
	if assignment.Subject.Delivery == nil || cause.FailedAssignmentID != assignment.ID || cause.FailedVerificationRunID != assignment.Run || cause.Repository != assignment.Repository || cause.ProjectID != assignment.ProjectID || cause.Delivery != *assignment.Subject.Delivery {
		return nil, errors.New("repair progress differs from its failed verification")
	}
	view := &factoryDeliveryRepairProgress{CauseID: factory.DeliveryRepairCauseID(assignment.Run), CycleID: cycle.ID, FailedRun: assignment.Run, SourceCommit: cause.Delivery.SourceCommit, State: cycle.State, Reason: cycle.Reason, RunID: cycle.RunID, Failures: cause.Report.Failures[:min(8, len(cause.Report.Failures))], FailureCount: len(cause.Report.Failures)}
	if cycle.ProposalOption != "" {
		if cycle.ProposalOption != factory.ReassessmentOptionAccept && cycle.ProposalOption != factory.ReassessmentOptionNeedsAttention || math.IsNaN(cycle.ProposalProbability) || math.IsInf(cycle.ProposalProbability, 0) || cycle.ProposalProbability < 0 || cycle.ProposalProbability > 1 || math.IsNaN(cycle.ProposalConfidence) || math.IsInf(cycle.ProposalConfidence, 0) || cycle.ProposalConfidence < 0 || cycle.ProposalConfidence > 1 {
			return nil, errors.New("repair progress has an invalid judgment")
		}
		view.Judgment = &factoryDeliveryRepairJudgment{Option: cycle.ProposalOption, Probability: cycle.ProposalProbability, Confidence: cycle.ProposalConfidence}
	}
	return view, nil
}
