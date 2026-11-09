package factory

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory/execution"
	"github.com/levitateos/factory/pkg/project"
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
		// Remote controller claims need their native connector bridge. A local
		// launch cannot silently ignore an explicit external execution grant.
		if r.Admission.Policy.Execution != nil {
			return empty, execution.ErrLaunch
		}
		// Historical same-user evidence remains readable, but it cannot authorize
		// a new desktop agent launch. The selected execution boundary requires a
		// broker run captured before the isolated worker is started.
		if r.Admission.Native == nil || !r.Admission.Native.Active() || !r.Admission.Native.MatchesRecord(*r.Native) || r.Native.Sealed || r.Native.Retirement == execution.Retired {
			return empty, execution.ErrLaunch
		}
		if a.NativeTool != nil && *a.NativeTool != r.Admission.Native.Tool {
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
		if r.Admission.Policy.Execution != nil {
			request.Appliance.ControllerGrantID = r.Admission.Policy.Execution.ControllerGrantID
			captured, err := ControllerAdmissionDigest(a, r)
			if err != nil || captured != r.Admission.ControllerManifestDigest {
				return empty, execution.ErrLaunch
			}
			request.Appliance.ControllerManifestDigest = r.Admission.ControllerManifestDigest
		}
	}
	if request.Validate() != nil {
		return empty, execution.ErrLaunch
	}
	return request, nil
}

// ControllerAdmissionDigest binds the immutable execution packet before native
// I/O. Later observations and private MCP credentials are outside this digest.
// Recovery uses the stored digest, never a newly selected policy or grant.
func ControllerAdmissionDigest(a Assignment, r Run) (string, error) {
	if r.Admission == nil || r.Admission.Policy.Execution == nil || !ValidID(r.ID) || a.Run != r.ID || a.Role != r.Role || a.ProjectID != r.ProjectID || !digest.MatchString(a.PromptSHA) {
		return "", errors.New("invalid controller admission packet")
	}
	admission := *r.Admission
	admission.ControllerManifestDigest = ""
	raw, err := json.Marshal(struct {
		Run, Project, Role, Source, Preparation, Phase, Prompt, Connection, Harness, Model, Image string
		Actor                                                                                     int64
		Deadline                                                                                  time.Time
		Admission                                                                                 RunAdmission
		Execution, Verification                                                                   []byte
	}{r.ID, r.ProjectID, r.Role, r.InputSHA, a.Preparation, a.Phase, a.PromptSHA, a.Connection, r.Harness, r.Model, r.Image, a.ActorID, r.Deadline, admission, r.ExecutionManifest, r.VerificationManifest})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
