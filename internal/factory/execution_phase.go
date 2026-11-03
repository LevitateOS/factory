// Execution phase bindings: which account role, subject kind and manifest
// each admitted phase requires. Phase values live on the Project wire
// (project.Execution*); this file owns the domain rules that bind them.
// FW11 admission records an explicit phase on every assignment and run;
// FW13 enforces profile inheritance and the manifest digest at launch.
package factory

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/levitateos/sodaos/internal/project"
)

// PhaseRole returns the single factory account role admitted for phase.
// Review and verification use the reviewer account; other phases use coder, including reassess and repair.
// Semantic profiles remain phase-bound. Do not infer phase from role.
func PhaseRole(phase string) (string, error) {
	switch phase {
	case project.ExecutionDecompose, project.ExecutionInvestigate,
		project.ExecutionImplement, project.ExecutionCorrect,
		project.ExecutionReassess, project.ExecutionRepair:
		return project.RoleCoder, nil
	case project.ExecutionReview, project.ExecutionVerify:
		return project.RoleReviewer, nil
	default:
		return "", errors.New("unknown execution phase")
	}
}

// ValidPhaseRole reports whether role is the admitted account for phase.
func ValidPhaseRole(phase, role string) bool {
	want, err := PhaseRole(phase)
	return err == nil && want == role
}

// SubjectKindAllowed reports whether a subject kind may enter phase. Intent
// subjects enter only through decompose; issue subjects never decompose,
// reassess, repair or verify; D13 completion subjects enter only through
// reassess, which accepts nothing else; D15 stall and D16 delivery_repair
// subjects enter only through repair; D16 delivery subjects enter only
// through verify. Planning phases carry no native publication or mutation
// capability.
func SubjectKindAllowed(kind, phase string) bool {
	switch kind {
	case SubjectIntent:
		return phase == project.ExecutionDecompose
	case SubjectIssue:
		switch phase {
		case project.ExecutionInvestigate, project.ExecutionImplement,
			project.ExecutionReview, project.ExecutionCorrect:
			return true
		default:
			return false
		}
	case SubjectCompletion:
		return phase == project.ExecutionReassess
	case SubjectStall, SubjectDeliveryRepair:
		return phase == project.ExecutionRepair
	case SubjectDelivery:
		return phase == project.ExecutionVerify
	default:
		return false
	}
}

// AdvancementID derives the deterministic identity for one automatic phase
// advancement. Uniqueness on (parent_assignment, parent_result_digest,
// next_phase) makes automatic advancement replay-safe: the same completed
// parent replays to the same child instead of launching twice.
func AdvancementID(parentAssignment, parentResultDigest, nextPhase string) (string, error) {
	if !ValidID(parentAssignment) {
		return "", errors.New("invalid advancement parent assignment")
	}
	if len(parentResultDigest) != 64 || strings.Trim(parentResultDigest, "0123456789abcdef") != "" {
		return "", errors.New("invalid advancement parent result digest")
	}
	if !project.ValidExecutionPhase(nextPhase) {
		return "", errors.New("invalid advancement next phase")
	}
	sum := sha256.Sum256([]byte("factory-advancement-v1\x00" + parentAssignment + "\x00" + parentResultDigest + "\x00" + nextPhase))
	return hex.EncodeToString(sum[:])[:32], nil
}

// Reasoning efforts admitted in a recorded execution profile. The pinned
// launchers hard-code low today; xhigh is the crew's qualified setup.
// Vocabulary admission here does not prove a harness supports an effort: a
// qualified profile must support the exact chosen effort, and support rides
// the proved harness pin the executor admits.
const (
	EffortLow    = "low"
	EffortMedium = "medium"
	EffortHigh   = "high"
	EffortXHigh  = "xhigh"
)

// ValidEffort reports whether effort is admitted profile vocabulary.
func ValidEffort(effort string) bool {
	switch effort {
	case EffortLow, EffortMedium, EffortHigh, EffortXHigh:
		return true
	default:
		return false
	}
}

// DefaultEffort and DefaultContributor are the product defaults recorded at
// policy creation when the operator omits the execution profile. Running
// snapshots are always explicit.
const (
	DefaultEffort      = EffortLow
	DefaultContributor = false
)

// ExecutionProfile records the admitted model execution shape for one role:
// explicit reasoning effort and Contributor mode. It replaces hard-coded
// launcher flags. Children inherit the exact parent profile and account.
type ExecutionProfile struct {
	Effort      string `json:"effort,omitempty"`
	Contributor bool   `json:"contributor,omitempty"`
}

// Validate checks vocabulary only. Contributor mode is valid only for Muse;
// harness support for the exact effort rides the proved harness pin.
func (p ExecutionProfile) Validate(harness string) error {
	if !ValidEffort(p.Effort) {
		return errors.New("invalid execution effort")
	}
	if p.Contributor && harness != project.FactoryHarnessMuse {
		return errors.New("contributor mode is valid only for Muse")
	}
	return nil
}

// EffectiveExecutionProfile resolves an omitted profile to the current
// product defaults at policy creation. Explicit values are kept as recorded,
// including an explicitly recorded default.
func EffectiveExecutionProfile(profile *ExecutionProfile) ExecutionProfile {
	if profile == nil {
		return ExecutionProfile{Effort: DefaultEffort, Contributor: DefaultContributor}
	}
	out := *profile
	if out.Effort == "" {
		out.Effort = DefaultEffort
	}
	return out
}
