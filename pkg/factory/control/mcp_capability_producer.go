package control

// Dispatch prepares the role-scoped capability before starting the native
// harness. The host receives its private relay binding separately from prompt
// content. Authentication activates it only after observing the matching live
// native run and rechecking current dispatch and preparation stop state.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/execution"
	"github.com/levitateos/factory/pkg/project"
	"github.com/levitateos/factory/pkg/store"
)

// mcpCapabilityGeneration orders re-deliveries of the same run.
// Dispatch produces exactly one capability per run, so the first
// generation is the only generation; exact restart delivery replays
// it identically, never a new deadline or allowance.
const mcpCapabilityGeneration = 1

// mcpProfileForPhase maps one dispatch execution phase to its
// admitted MCP profile through the exact canonical phase/profile
// mapping. Unknown, legacy, or pre-phase input refuses with no MCP
// authority: nothing defaults to Builder or Inspector, and the caller
// role never substitutes for the recorded phase.
func mcpProfileForPhase(phase string) (factory.MCPProfile, error) {
	switch phase {
	case project.ExecutionDecompose:
		return factory.MCPProfileProductLead, nil
	case project.ExecutionInvestigate:
		return factory.MCPProfileArchitect, nil
	case project.ExecutionImplement:
		return factory.MCPProfileBuilder, nil
	case project.ExecutionReview:
		return factory.MCPProfileInspector, nil
	case project.ExecutionCorrect:
		return factory.MCPProfileFixer, nil
	case project.ExecutionReassess:
		return factory.MCPProfileStrategist, nil
	case project.ExecutionRepair:
		return factory.MCPProfileStrategist, nil
	case project.ExecutionVerify:
		return factory.MCPProfileTestPilot, nil
	}
	return "", errors.New("mcp producer refuses a phase without MCP authority")
}

// mcpAuthorityDigest binds the recorded grant revisions behind one
// dispatch: the assignment authority fields plus the connection and
// actor the packet recorded. The digest carries revisions, never
// secret bytes, and changes whenever the recorded authority does,
// so a replayed capability cannot borrow edited authority.
func mcpAuthorityDigest(a factory.Assignment) string {
	authority := a.Authority
	fields := []string{
		authority.RequirementsID, authority.ApprovalID, authority.SponsorshipConnection,
		strconv.FormatInt(authority.Policy, 10),
		strconv.FormatInt(authority.Operator, 10),
		strconv.FormatInt(authority.Capacity, 10),
		strconv.FormatInt(authority.Sponsorship, 10),
		strconv.FormatInt(authority.Environment, 10),
		a.Connection,
		strconv.FormatInt(a.ActorID, 10),
	}
	sum := sha256.Sum256([]byte(strings.Join(fields, "\n")))
	return hex.EncodeToString(sum[:])
}

// mcpSubjectForAssignment names the recorded scope the capability
// serves: the canonical subject key when the assignment carries one,
// otherwise the bare issue scope. Either way the value is short,
// non-empty and bound to the recorded repository and issue.
func mcpSubjectForAssignment(a factory.Assignment) string {
	if key := a.Subject.SubjectKey(a.Repository); key != "" {
		return key
	}
	return "issue:" + strconv.FormatInt(a.Repository, 10) + ":" + strconv.FormatInt(a.Issue, 10)
}

// BuildMCPCapability derives one staged capability from recorded
// state only: the assignment, its packet-settled run and the
// recorded preparation. The capability ID is the run identity, so
// one run owns exactly one capability and exact restart delivery
// replays the same staged bytes; a changed scope under that ID
// conflicts at staging instead of replacing authority. Accepted
// revision is the recorded native revision the assignment was
// admitted against; the deadline is the settled run deadline.
func BuildMCPCapability(a factory.Assignment, run factory.Run, preparation execution.PreparationRecord) (factory.MCPCapability, error) {
	if !factory.ValidID(run.ID) {
		return factory.MCPCapability{}, errors.New("mcp producer refuses a run without recorded identity")
	}
	if !factory.ValidID(a.ID) || a.Repository < 1 {
		return factory.MCPCapability{}, errors.New("mcp producer refuses an assignment without recorded scope")
	}
	if !factory.ValidProjectID(a.ProjectID) {
		return factory.MCPCapability{}, errors.New("mcp producer refuses an assignment without recorded project")
	}
	setup := preparation.Preparation
	if !factory.ValidDigest(setup.SetupDigest) {
		return factory.MCPCapability{}, errors.New("mcp producer refuses a preparation without recorded digest")
	}
	source := a.SourceCommit
	if source == "" {
		source = run.InputSHA
	}
	if source == "" {
		return factory.MCPCapability{}, errors.New("mcp producer refuses a run without recorded source")
	}
	if run.Deadline.IsZero() {
		return factory.MCPCapability{}, errors.New("mcp producer refuses a run without recorded deadline")
	}
	profile, err := mcpProfileForPhase(a.Phase)
	if err != nil {
		return factory.MCPCapability{}, err
	}
	if a.Phase == project.ExecutionDecompose && a.PlanningContext != nil && a.PlanningContext.IssueMaker != nil {
		if err := a.PlanningContext.IssueMaker.Validate(a.PlanningContext.Head); err != nil {
			return factory.MCPCapability{}, err
		}
		profile = factory.MCPProfileIssueMaker
	}
	subject := mcpSubjectForAssignment(a)
	if subject == "" || len(subject) > factory.MCPMaxRefChars {
		return factory.MCPCapability{}, errors.New("mcp producer refuses an unnameable recorded subject")
	}
	capability := factory.MCPCapability{
		ID:                run.ID,
		AssignmentID:      a.ID,
		RunID:             run.ID,
		Generation:        mcpCapabilityGeneration,
		Profile:           profile,
		Subject:           subject,
		Repository:        a.Repository,
		Project:           a.ProjectID,
		PreparationDigest: setup.SetupDigest,
		SourceDigest:      source,
		AcceptedRevision:  strconv.FormatInt(a.NativeRev, 10),
		Authority:         mcpAuthorityDigest(a),
		DeadlineUnix:      run.Deadline.Unix(),
		State:             factory.MCPCapabilityStaged,
	}
	if err := capability.Validate(); err != nil {
		return factory.MCPCapability{}, err
	}
	return capability, nil
}

// StageMCPCapabilityForLaunch records one staged capability and
// stops there: staged metadata only, never activation. The store
// replays identical staged bytes as a no-op and conflicts a changed
// scope under the same identity, so exact restart delivery is safe
// and authority can never be replaced through this path.
func StageMCPCapabilityForLaunch(ctx context.Context, st *store.Store, capability factory.MCPCapability) error {
	if st == nil {
		return errors.New("mcp producer has no store")
	}
	if err := capability.Validate(); err != nil {
		return err
	}
	if capability.State != factory.MCPCapabilityStaged {
		return errors.New("mcp producer stages only staged capabilities")
	}
	return st.StageMCPCapability(ctx, capability)
}

// recordedMCPPreparation loads the recorded preparation digest and
// stop state behind one assignment: the standing preparation first,
// then the retry-custody candidate record that shares the same table.
// Both are durable recorded state; no host observation is consulted.
func recordedMCPPreparation(ctx context.Context, st *store.Store, id string) (execution.PreparationRecord, bool, error) {
	if prep, err := st.Preparation(ctx, id); err == nil {
		return prep, prep.State.Stopped, nil
	} else if record, cerr := st.CandidatePreparation(ctx, id); cerr == nil {
		return record.Preparation, record.Stopped || record.Preparation.State.Stopped, nil
	} else {
		return execution.PreparationRecord{}, false, err
	}
}

// ProduceMCPCapabilityForLaunch stages the capability metadata for
// one packet-settled launch from recorded state only — never active.
// Stop intent is the recorded preparation stop plus the recorded
// dispatch-gate closure: either stages the retained history and
// reports the stop; admission stays refused while inactive. An
// unreadable stop signal refuses production rather than guessing.
// Production dispatch requires this to succeed before starting its harness.
func ProduceMCPCapabilityForLaunch(ctx context.Context, st *store.Store, a factory.Assignment, run factory.Run) error {
	if st == nil {
		return errors.New("mcp producer has no store")
	}
	preparation, stopped, err := recordedMCPPreparation(ctx, st, a.Preparation)
	if err != nil {
		return err
	}
	open, _, _, derr := st.DispatchState(ctx, a.Repository)
	if derr != nil {
		return derr
	}
	if !open {
		stopped = true
	}
	capability, err := BuildMCPCapability(a, run, preparation)
	if err != nil {
		return err
	}
	if serr := StageMCPCapabilityForLaunch(ctx, st, capability); serr != nil {
		return serr
	}
	if stopped {
		return ErrMCPCapabilityStopped
	}
	return nil
}
