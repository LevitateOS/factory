package factory

import (
	"errors"
	"math"

	"github.com/levitateos/sodaos/internal/project"
)

var (
	ErrVerificationAllowanceOwner    = errors.New("application verification allowance requires its intent owner")
	ErrVerificationAllowanceIncrease = errors.New("application verification allowance must explicitly increase")
	ErrVerificationAllowanceState    = errors.New("application verification allowance requires an accepted repository intent")
)

// AppVerificationLimits are a stable intent's cumulative allowance. Existing
// roots retain their admitted limits; every new root also obeys the fixed
// per-run cap and the current repository, sponsor and connection funding.
type AppVerificationLimits struct {
	Runs          int `json:"runs"`
	ActiveMinutes int `json:"active_minutes"`
}

func (l AppVerificationLimits) Validate() error {
	if l.Runs < 1 || l.Runs > math.MaxInt32 || l.ActiveMinutes < 1 || l.ActiveMinutes > math.MaxInt32 {
		return errors.New("invalid finite application verification limits")
	}
	return nil
}

type AppVerificationUsage struct {
	Runs          int   `json:"runs"`
	ActiveSeconds int64 `json:"active_seconds"`
}

type AppVerificationAllowance struct {
	Intent      string                `json:"intent"`
	Revision    string                `json:"revision"`
	Limits      AppVerificationLimits `json:"limits"`
	Consumption AppVerificationUsage  `json:"consumption"`
	Remaining   AppVerificationUsage  `json:"remaining"`
	CanIncrease bool                  `json:"can_increase"`
	CommandID   string                `json:"command_id,omitempty"`
	Repository  int64                 `json:"-"`
}

func (a *AppVerificationAllowance) UpdateRemaining() {
	a.Remaining.Runs = max(0, a.Limits.Runs-a.Consumption.Runs)
	a.Remaining.ActiveSeconds = max(0, int64(a.Limits.ActiveMinutes)*60-a.Consumption.ActiveSeconds)
}

type AppVerificationAllowanceChange struct {
	CommandID        string                `json:"command_id"`
	ExpectedRevision string                `json:"expected_revision"`
	Limits           AppVerificationLimits `json:"limits"`
}

func (c AppVerificationAllowanceChange) Validate() error {
	if !ValidID(c.CommandID) || c.ExpectedRevision != "" && !ValidID(c.ExpectedRevision) {
		return errors.New("invalid verification allowance command revision")
	}
	return c.Limits.Validate()
}

func AppVerificationAllowanceTarget(intent string) string {
	return "intent/" + intent + "/verification-allowance"
}

func ValidVerificationAllowanceIntent(intent string) bool { return project.ValidDecisionID(intent) }
