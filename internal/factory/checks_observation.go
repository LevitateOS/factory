package factory

import (
	"errors"
	"strings"
)

// MaxObservedCheckContext bounds one observed native check context.
const MaxObservedCheckContext = 256

// MaxObservedCheckState bounds one observed native check state.
const MaxObservedCheckState = 64

// ObservedCheck is the latest native status for one context on the exact
// head. Empty State means the context has no status on this head.
type ObservedCheck struct {
	Context string `json:"context"`
	State   string `json:"state,omitempty"`
}

func (c ObservedCheck) Validate() error {
	if c.Context == "" || len(c.Context) > MaxObservedCheckContext ||
		strings.IndexFunc(c.Context, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
		return errors.New("invalid observed check context")
	}
	if len(c.State) > MaxObservedCheckState ||
		strings.IndexFunc(c.State, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
		return errors.New("invalid observed check state")
	}
	return nil
}

// ObservedChecks is one bracketed native observation for a check target:
// the bound idle revision, the observed head/base tips, the latest status
// per context on the exact head, and whether anything required was hidden.
type ObservedChecks struct {
	Checks           []ObservedCheck `json:"checks,omitempty"`
	NativeRev        int64           `json:"native_revision"`
	ObservedContexts int             `json:"observed_contexts"`
	HeadTip          string          `json:"head_tip,omitempty"`
	BaseTip          string          `json:"base_tip,omitempty"`
	Complete         bool            `json:"complete"`
	Hidden           bool            `json:"hidden,omitempty"`
}

// Validate rejects malformed observations. Shape only: the reader binds
// the revision, tips and latest-per-context statuses from one bracket.
func (o ObservedChecks) Validate() error {
	if o.NativeRev < 1 || o.ObservedContexts < 0 || len(o.Checks) > SnapshotPageBound {
		return errors.New("invalid observed check evidence")
	}
	if o.HeadTip != "" && !ValidCommit(o.HeadTip) {
		return errors.New("invalid observed head tip")
	}
	if o.BaseTip != "" && !ValidCommit(o.BaseTip) {
		return errors.New("invalid observed base tip")
	}
	seen := make(map[string]bool, len(o.Checks))
	for _, check := range o.Checks {
		if err := check.Validate(); err != nil {
			return err
		}
		if seen[check.Context] {
			return errors.New("duplicate observed check context")
		}
		seen[check.Context] = true
	}
	return nil
}

// SnapshotPageBound mirrors the native snapshot page bound so
// observations stay within one complete native page.
const SnapshotPageBound = 50

// CheckResult is one required check's verdict: its latest observed native
// state on the exact head and whether that state passes. Empty State
// means the check is missing on this head.
type CheckResult struct {
	Context string `json:"context"`
	State   string `json:"state,omitempty"`
	Passed  bool   `json:"passed"`
}

func (r CheckResult) Validate() error {
	if r.Context == "" || len(r.Context) > MaxCheckName {
		return errors.New("invalid check result context")
	}
	if len(r.State) > MaxObservedCheckState {
		return errors.New("invalid check result state")
	}
	if r.Passed != (r.State == CheckStateSuccess) {
		return errors.New("check result pass differs from its success state")
	}
	return nil
}

// CheckStateSuccess is the only native state that passes a required
// check. Every other state, including a missing status, is not a pass.
const CheckStateSuccess = "success"

// checkStateSeverity ranks non-pass states for reason selection. Higher
// ranks are definitive failures; pending and missing only wait for CI.
func checkStateSeverity(state string) (rank int, reason string) {
	switch state {
	case CheckStateSuccess:
		return 0, ""
	case "failure":
		return 70, CheckReasonFailed
	case "error":
		return 60, CheckReasonError
	case "cancelled":
		return 50, CheckReasonCancelled
	case "skipped":
		return 40, CheckReasonSkipped
	case "warning":
		return 20, CheckReasonWarning
	case "pending", "":
		return 10, CheckReasonPending
	default:
		return 30, CheckReasonUnknownState
	}
}
