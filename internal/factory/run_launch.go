package factory

import (
	"bytes"

	"github.com/levitateos/sodaos/internal/collaboration"
	"github.com/levitateos/sodaos/internal/factory/execution"
	"github.com/levitateos/sodaos/internal/project"
)

// LaunchRequest uses only this run's captured admission. A historical/unplaced
// record remains inspectable but cannot adopt today's executable or placement.
func (r Run) LaunchRequest(a Assignment, input project.FactoryLaunch) (execution.LaunchRequest, error) {
	var empty execution.LaunchRequest
	if r.Validate() != nil || a.Validate() != nil || a.Stage != AssignmentAssigned || a.Run != r.ID || r.Outcome != "" || r.Reconciled || r.ContextPending || r.Admission == nil || input.Validate() != nil || input.Run.ID != r.ID || input.Run.Project != r.ProjectID || input.Run.Role != r.Role || input.Run.SourceCommit != r.InputSHA || !input.Run.Deadline.Equal(r.Deadline) || input.Run.Model != r.Model || input.Run.Model != a.Model ||
		input.Run.Project != a.ProjectID || input.Run.Role != a.Role || input.Run.SourceCommit != a.SourceCommit || input.Run.Preparation != a.Preparation || input.Run.Actor != a.ActorID || input.Run.Connection != a.Connection || input.Run.ExecutionPhase != a.Phase || input.Run.Assignment != a.PromptSHA || !bytes.Equal(input.Prompt, a.Prompt) || !bytes.Equal(input.ExecutionManifest, r.ExecutionManifest) || !bytes.Equal(input.VerificationManifest, r.VerificationManifest) {
		return empty, execution.ErrLaunch
	}
	if input.Run.Harness == project.FactoryHarnessMuse {
		selection := r.Admission.Policy.Roles[r.Role]
		profile := selection.Profile
		if input.Run.Effort != profile.Effort || input.Run.Contributor != profile.Contributor {
			return empty, execution.ErrLaunch
		}
	}
	request := execution.LaunchRequest{Input: input, Kind: r.RuntimeKind()}
	if r.Native != nil {
		if r.Admission.Native == nil || !r.Admission.Native.MatchesRecord(*r.Native) || r.Native.Sealed || r.Native.Retirement == execution.Retired {
			return empty, execution.ErrLaunch
		}
		native := *r.Admission.Native
		request.Native = &native
	} else {
		if r.Admission.Profile == nil || r.Admission.Tool == nil {
			return empty, execution.ErrLaunch
		}
		tool := r.Admission.Tool
		request.Kind = collaboration.Appliance
		request.Appliance = &execution.ApplianceLaunch{Profile: *r.Admission.Profile, Tool: execution.ApplianceTool{Harness: tool.Harness, Version: tool.Version, SHA256: tool.SHA256, Image: tool.Image}}
	}
	if request.Validate() != nil {
		return empty, execution.ErrLaunch
	}
	return request, nil
}
