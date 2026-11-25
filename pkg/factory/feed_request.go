package factory

import (
	"errors"
	"fmt"
	"slices"
)

// Factory settings sections: the one list of settings a missing
// authorization names (service accessRequiredActions) and an agent's request
// may point at. The shared Settings page links by these ids.
const (
	SettingPolicy           = "policy"
	SettingOperatorGrant    = "operator_grant"
	SettingCapacity         = "capacity"
	SettingSponsorships     = "sponsorships"
	SettingEnvironmentGrant = "environment_grant"
	SettingProject          = "project"
	SettingLifecycle        = "lifecycle"
)

// Agent requests (slice 5.4). An ask may carry a request for more allowance
// or for access. The post explains what is asked and why and points at the
// setting; it carries no grant control and grants, changes or pre-fills
// nothing. The person changes the setting in Settings. State is served on
// every read from the recorded setting and the ask's replies (store
// feedRequestMetSQL), never submitted.
const (
	RequestAllowance = "allowance"
	RequestAccess    = "access"
)

// FeedRequest is one agent request. Amount is the total the setting needs to
// reach, in the setting's own unit; only allowance requests carry it.
type FeedRequest struct {
	Kind    string `json:"kind"`
	Setting string `json:"setting"`
	Amount  int    `json:"amount,omitempty"`
	Unit    string `json:"unit,omitempty"`
	Reason  string `json:"reason"`
	// Blocks names the work that waits on the request, in one line.
	Blocks string `json:"blocks,omitempty"`
	// State is served: open, met (the recorded setting now satisfies it) or
	// replied (someone replied to the ask).
	State string `json:"state,omitempty"`
}

// requestSettings lists which requests each setting can satisfy: access, and
// allowance in its unit up to the setting's own save bound (Capacity
// validation). Settings absent here take no request.
var requestSettings = map[string]struct {
	access bool
	unit   string
	max    int
}{
	SettingPolicy:           {access: true},
	SettingOperatorGrant:    {access: true},
	SettingEnvironmentGrant: {access: true},
	SettingSponsorships:     {access: true},
	SettingCapacity:         {unit: "runs", max: 64},
}

var settingIDs = []string{SettingPolicy, SettingOperatorGrant, SettingCapacity, SettingSponsorships, SettingEnvironmentGrant, SettingProject, SettingLifecycle}

// Validate admits one submitted request.
func (r FeedRequest) Validate() error {
	if r.State != "" {
		return errors.New("request state is served, not submitted")
	}
	if !slices.Contains(settingIDs, r.Setting) {
		return fmt.Errorf("unknown setting %q", r.Setting)
	}
	setting, ok := requestSettings[r.Setting]
	switch {
	case !ok:
		return fmt.Errorf("setting %s takes no request", r.Setting)
	case r.Kind == RequestAccess:
		if !setting.access {
			return fmt.Errorf("setting %s grants no access", r.Setting)
		}
		if r.Amount != 0 || r.Unit != "" {
			return errors.New("an access request carries no amount")
		}
	case r.Kind == RequestAllowance:
		if setting.unit == "" {
			return fmt.Errorf("setting %s holds no allowance", r.Setting)
		}
		if r.Unit != setting.unit || r.Amount < 1 || r.Amount > setting.max {
			return fmt.Errorf("setting %s takes an amount of 1–%d %s", r.Setting, setting.max, setting.unit)
		}
	default:
		return errors.New("request kind is allowance or access")
	}
	if err := plainText(r.Reason, "request reason", 400, true); err != nil {
		return err
	}
	return plainText(r.Blocks, "request blocks", 160, false)
}
