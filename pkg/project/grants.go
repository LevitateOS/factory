// Standing environment and preparation authority: the owner's per-repository
// environment grant plus the maintainer's requirement acceptance and the
// administrator's privileged-effect approval as immutable decisions. Either
// preparation reference alone authorizes nothing. This file is pure
// validation: no I/O, no SQL, no privilege.
package project

import (
	"errors"
	"maps"

	"github.com/levitateos/factory/pkg/collaboration"
)

// NativeEnvironment is the configured local controller, not an appliance image
// or an arbitrary host path. Each preparation captures its own checkout owner.
type NativeEnvironment struct {
	Profile    collaboration.ProfileID `json:"profile"`
	Runtime    collaboration.RuntimeID `json:"runtime"`
	Generation int64                   `json:"generation,string"`
}

func (n NativeEnvironment) Validate() error {
	if !collaboration.ValidID(string(n.Profile)) || !collaboration.ValidID(string(n.Runtime)) || n.Generation <= 0 {
		return errors.New("invalid native environment")
	}
	return nil
}

// EnvironmentGrant is the current repository owner's standing permission to
// create and start the Project environment automatically. It creates no
// membership and grants no factory execution by itself; Project records the
// created Project once it exists. Repository and Owner are canonical Factory
// identities; a native Project retains its separate native owner.
type EnvironmentGrant struct {
	Project    string             `json:"project,omitempty"`
	Repository int64              `json:"repository,string"`
	Revision   int64              `json:"revision"`
	Owner      int64              `json:"owner,string"`
	Profile    *Profile           `json:"profile"`
	Active     bool               `json:"active"`
	Native     *NativeEnvironment `json:"native,omitempty"`
}

func (g EnvironmentGrant) Validate() error {
	if g.Repository <= 0 || g.Revision < 0 || g.Owner <= 0 ||
		(g.Native == nil && (g.Profile == nil || g.Profile.Validate() != nil)) ||
		(g.Native != nil && (g.Profile != nil || g.Native.Validate() != nil)) {
		return errors.New("invalid environment grant")
	}
	if g.Project != "" && !ValidID(g.Project) {
		return errors.New("invalid environment grant project reference")
	}
	return nil
}

// RequirementDecision is the code-write maintainer's immutable acceptance of
// exact environment requirements: the approved-base source commit and the
// effective setup/input digests. Decisions form a predecessor chain per
// Project; only the head authorizes new preparation. It grants neither
// Project root nor host execution.
type RequirementDecision struct {
	ID           string             `json:"id"`
	Predecessor  string             `json:"predecessor,omitempty"`
	Project      string             `json:"project"`
	SourceCommit string             `json:"source_commit"`
	SetupDigest  string             `json:"setup_digest"`
	InputsDigest string             `json:"inputs_digest"`
	Approver     int64              `json:"approver"`
	SourceInputs map[string]string  `json:"source_inputs,omitempty"`
	Native       *NativeEnvironment `json:"native,omitempty"`
	Recipe       *SetupRecipe       `json:"recipe,omitempty"`
}

// SetupRecipe retains what the local owner actually reviewed, without source
// bundle bytes or a claim that any command has run.
type SetupRecipe struct {
	Files map[string][]byte `json:"files"`
	Tools []string          `json:"tools"`
}

func (d RequirementDecision) Validate() error {
	if d.Recipe != nil {
		if d.Native == nil || (ApprovedSetup{Files: d.Recipe.Files}).ValidateFiles() != nil || len(d.Recipe.Tools) > MaxPrepareTools || d.SetupDigest != SetupDigestOf(d.Recipe.Files) {
			return errors.New("invalid reviewed setup recipe")
		}
		names := make([]string, 0, len(d.Recipe.Files))
		for name := range d.Recipe.Files {
			names = append(names, name)
		}
		for _, tool := range d.Recipe.Tools {
			if !ValidToolName(tool) {
				return errors.New("invalid reviewed setup tool")
			}
		}
		if d.InputsDigest != PreparationInputsDigestOf(names, d.SourceInputs, d.Recipe.Tools) {
			return errors.New("reviewed setup inputs differ from acceptance")
		}
	}
	if d.Native != nil && d.Native.Validate() != nil {
		return errors.New("invalid native requirement owner")
	}
	if !ValidDecisionID(d.ID) || (d.Predecessor != "" && !ValidDecisionID(d.Predecessor)) || d.Predecessor == d.ID {
		return errors.New("invalid requirement decision identity")
	}
	if !ValidID(d.Project) || d.Approver <= 0 || !ValidCommit(d.SourceCommit) || !ValidDigest(d.SetupDigest) || !ValidDigest(d.InputsDigest) {
		return errors.New("invalid requirement decision inputs")
	}
	return ValidateSourceInputs(d.SourceInputs)
}

// Ref converts the decision to the preparation requirement reference ST01
// records. The revision counts chain depth from the stored head.
func (d RequirementDecision) Ref(depth int64) RequirementAcceptance {
	return RequirementAcceptance{ID: d.ID, Revision: depth, Approver: d.Approver, SourceCommit: d.SourceCommit, Digest: d.InputsDigest, SourceInputs: maps.Clone(d.SourceInputs)}
}

// ApprovalDecision is the native Project administrator's immutable approval
// of reviewed privileged effects for one accepted requirement: the exact
// installed tool/service/resource results and readiness evidence. Verified
// asserts the administrator inspected the live Project state.
type ApprovalDecision struct {
	ID              string `json:"id"`
	Predecessor     string `json:"predecessor,omitempty"`
	Project         string `json:"project"`
	Requirement     string `json:"requirement"`
	EffectsDigest   string `json:"effects_digest"`
	ReadinessDigest string `json:"readiness_digest"`
	Approver        int64  `json:"approver"`
	Verified        bool   `json:"verified"`
	// Reviewed records native owner consent to intended isolated setup effects.
	// It does not assert that setup has run or installed readiness was proved.
	Reviewed bool               `json:"reviewed,omitempty"`
	Native   *NativeEnvironment `json:"native,omitempty"`
}

func (d ApprovalDecision) Validate() error {
	if d.Reviewed && d.Native == nil {
		return errors.New("local setup review requires a native execution owner")
	}
	if d.Native != nil && d.Native.Validate() != nil {
		return errors.New("invalid native approval owner")
	}
	if !ValidDecisionID(d.ID) || (d.Predecessor != "" && !ValidDecisionID(d.Predecessor)) || d.Predecessor == d.ID {
		return errors.New("invalid privileged-effect approval identity")
	}
	if !ValidID(d.Project) || !ValidDecisionID(d.Requirement) || d.Approver <= 0 ||
		!ValidDigest(d.EffectsDigest) || (!ValidDigest(d.ReadinessDigest) && !(d.Native != nil && d.Reviewed && d.ReadinessDigest == "")) {
		return errors.New("invalid privileged-effect approval inputs")
	}
	return nil
}

func (d ApprovalDecision) AllowsPreparation() bool {
	return d.Verified || d.Native != nil && d.Reviewed
}

// Ref converts the decision to the preparation approval reference ST01 records.
func (d ApprovalDecision) Ref(depth int64) AdminApproval {
	return AdminApproval{ID: d.ID, Revision: depth, Approver: d.Approver, EffectsDigest: d.EffectsDigest}
}
