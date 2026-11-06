package service

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/execution"
	"github.com/levitateos/factory/pkg/project"
	"github.com/levitateos/factory/pkg/store"
)

// WorkerAdmission consumes an authenticated capability and observes its recorded
// worker. Inspect supplies runtime facts, never a new launch or credential.
// Activation and current stop/preparation gates remain in the existing store.
type WorkerAdmission struct {
	Store   *store.Store
	Inspect func(context.Context, execution.RunAddress) (execution.RunObservation, error)
}

var (
	ErrWorkerUnavailable = errors.New("worker observation is unavailable")
	ErrWorkerBinding     = errors.New("worker no longer matches its admitted capability")
	ErrWorkerObservation = errors.New("worker is not running in its admitted environment")
)

func (s WorkerAdmission) current(ctx context.Context, admitted factory.MCPCapability) (factory.MCPCapability, factory.Assignment, factory.Run, error) {
	var empty factory.MCPCapability
	var assignment factory.Assignment
	var run factory.Run
	cap, err := s.Store.GetMCPCapability(ctx, admitted.ID)
	if err != nil {
		return empty, assignment, run, err
	}
	if cap.Validate() != nil || cap.ScopeDigest() != admitted.ScopeDigest() || cap.Expired(time.Now()) || (cap.State != factory.MCPCapabilityStaged && cap.State != factory.MCPCapabilityActive) {
		return empty, assignment, run, ErrWorkerBinding
	}
	if err := s.Store.CheckMCPWorkActive(ctx, cap); err != nil {
		return empty, assignment, run, err
	}
	assignment, err = s.Store.Assignment(ctx, cap.AssignmentID)
	if err != nil {
		return empty, assignment, run, err
	}
	if assignment.ID != cap.AssignmentID || assignment.Run != cap.RunID || assignment.ProjectID != cap.Project || assignment.Repository != cap.Repository || assignment.SourceCommit != cap.SourceDigest || assignment.Subject.SubjectKey(assignment.Repository) != cap.Subject || strconv.FormatInt(assignment.NativeRev, 10) != cap.AcceptedRevision {
		return empty, assignment, run, ErrWorkerBinding
	}
	run, err = s.Store.FactoryRun(ctx, cap.RunID)
	if err != nil {
		return empty, assignment, run, err
	}
	if run.ID != cap.RunID || run.ProjectID != cap.Project || run.Role != assignment.Role || run.InputSHA != assignment.SourceCommit || run.Reconciled || run.Outcome != "" || (!run.Deadline.IsZero() && !time.Now().Before(run.Deadline)) {
		return empty, assignment, run, ErrWorkerBinding
	}
	return cap, assignment, run, ctx.Err()
}

func workerObservation(run factory.Run, observation execution.RunObservation) error {
	address := execution.RunAddress{Run: run.ID, Workspace: run.ProjectID}
	if observation.ValidateFor(address) != nil || observation.Kind != run.RuntimeKind() || !observation.Live || observation.Phase != project.FactoryRunning || observation.Retirement != execution.Running || observation.Role != run.Role || observation.ExitCode != nil {
		return ErrWorkerObservation
	}
	if run.Native != nil {
		if run.Native.Sealed || run.Native.Provenance.Process == nil {
			return ErrWorkerObservation
		}
		if observation.Native.Process == nil || observation.Native.Process.ExitCode != nil || observation.Native.Process.ExitSignal != nil {
			return ErrWorkerObservation
		}
		next := *run.Native
		next.Provenance, next.Retirement = *observation.Native, observation.Retirement
		if !next.Advances(*run.Native) {
			return ErrWorkerObservation
		}
		return nil
	}
	if observation.Kind != collaboration.Appliance || observation.Appliance == nil {
		return ErrWorkerObservation
	}
	actual := observation.Appliance.Invocation
	if actual.Generation < 1 || actual.Unit == "" || actual.Container == "" || actual.Invocation == "" || (run.IdentityGeneration > 0 && run.IdentityGeneration != actual.Generation) {
		return ErrWorkerObservation
	}
	if invocation := run.InvocationID(); invocation != "" && invocation != actual.Invocation {
		return ErrWorkerObservation
	}
	if recorded := run.ApplianceInvocation; recorded != nil && !actual.Advances(*recorded) {
		return ErrWorkerObservation
	}
	return nil
}

// Prepare activates only a currently observed, matching worker. Every request
// reloads current records after I/O, so a cached credential/observation cannot
// outlive revocation, stop or a different captured process incarnation.
func (s WorkerAdmission) Prepare(ctx context.Context, admitted factory.MCPCapability) (factory.MCPCapability, error) {
	var empty factory.MCPCapability
	if s.Store == nil || s.Inspect == nil {
		return empty, ErrWorkerUnavailable
	}
	if admitted.Validate() != nil || (admitted.State != factory.MCPCapabilityStaged && admitted.State != factory.MCPCapabilityActive) {
		return empty, ErrWorkerBinding
	}
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cap, _, run, err := s.current(bounded, admitted)
	if err != nil {
		return empty, err
	}
	observation, err := s.Inspect(bounded, execution.RunAddress{Run: run.ID, Workspace: run.ProjectID})
	if err != nil {
		return empty, errors.Join(ErrWorkerObservation, err)
	}
	// A native inspector records its first exact process observation before
	// activation. Validate against the reloaded row, including that first fact.
	cap, _, latest, err := s.current(bounded, admitted)
	if err != nil {
		return empty, err
	}
	if err := workerObservation(latest, observation); err != nil {
		return empty, err
	}
	if err := bounded.Err(); err != nil {
		return empty, err
	}
	if cap.State == factory.MCPCapabilityStaged {
		if err := s.Store.ActivateObservedMCPCapability(bounded, cap); err != nil {
			return empty, err
		}
	}
	// The transaction owns final activation/stop checks. Return its current
	// record, including concurrent retirement, instead of the supplied snapshot.
	cap, _, _, err = s.current(bounded, admitted)
	if err != nil {
		return empty, err
	}
	return cap, nil
}
