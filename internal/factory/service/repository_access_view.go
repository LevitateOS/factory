package service

import "github.com/levitateos/sodaos/internal/factory"

// accessEntryView reports whether the viewer may save one settings
// section, with the bounded refusal reason when not. The owning PUT
// APIs revalidate at save time.
type accessEntryView struct {
	Reason   string `json:"reason,omitempty"`
	Editable bool   `json:"editable"`
}

// accessEditableReasons are the bounded codes behind one editability
// entry. They mirror the owning PUT admission rules.
const (
	accessReasonSession    = "session_changed"
	accessReasonOwnerAdmin = "owner_or_admin_required"
	accessReasonOperator   = "operator_required"
	accessReasonRepoOwner  = "repository_owner_required"
	accessReasonConnOwner  = "connection_owner_required"
	accessReasonIdentity   = "identity_unavailable"
)

// RepositoryViewer renders the viewer's current standing: host-verified
// repository powers, runtime operator permission, workspace membership
// and session currency. Each boundary stays separate; no power implies
// another.
type RepositoryViewer struct {
	CodeWrite        bool `json:"code_write"`
	OwnerOrAdmin     bool `json:"owner_or_admin"`
	EnvironmentOwner bool `json:"environment_owner"`
	Operator         bool `json:"operator"`
	Member           bool `json:"member"`
	SessionCurrent   bool `json:"session_current"`
}

// accessConnectionEntryView reports editability for one connection-owned
// section: sponsorships admit the connection owner.
type accessConnectionEntryView struct {
	Connection string `json:"connection"`
	Reason     string `json:"reason,omitempty"`
	Editable   bool   `json:"editable"`
}

// accessEditableView maps every Factory settings section to the viewer's
// current save power. Values live on the status route; this view carries
// powers only, never a second copy of the settings.
type accessEditableView struct {
	Sponsorships     []accessConnectionEntryView `json:"sponsorships"`
	Policy           accessEntryView             `json:"policy"`
	ActivitySettings accessEntryView             `json:"activity_settings"`
	OperatorGrant    accessEntryView             `json:"operator_grant"`
	Capacity         accessEntryView             `json:"capacity"`
	EnvironmentGrant accessEntryView             `json:"environment_grant"`
}

type accessEditableInput struct {
	Viewer      RepositoryViewer
	Connections []string
	Owned       map[string]bool
	IdentityOK  bool
}

func accessEditableDTO(in accessEditableInput) accessEditableView {
	view := accessEditableView{Sponsorships: []accessConnectionEntryView{}}
	session := ""
	if !in.Viewer.SessionCurrent {
		session = accessReasonSession
	}
	gated := func(allowed bool, reason string) accessEntryView {
		if session != "" {
			return accessEntryView{Reason: session}
		}
		if !allowed {
			return accessEntryView{Reason: reason}
		}
		return accessEntryView{Editable: true}
	}
	view.Policy = gated(in.Viewer.OwnerOrAdmin, accessReasonOwnerAdmin)
	view.ActivitySettings = gated(in.Viewer.OwnerOrAdmin, accessReasonOwnerAdmin)
	view.OperatorGrant = gated(in.Viewer.Operator, accessReasonOperator)
	view.Capacity = gated(in.Viewer.Operator, accessReasonOperator)
	view.EnvironmentGrant = gated(in.Viewer.EnvironmentOwner, accessReasonRepoOwner)
	for _, connection := range in.Connections {
		entry := accessConnectionEntryView{Connection: connection}
		switch {
		case session != "":
			entry.Reason = session
		case !in.IdentityOK:
			entry.Reason = accessReasonIdentity
		case !in.Owned[connection]:
			entry.Reason = accessReasonConnOwner
		default:
			entry.Editable = true
		}
		view.Sponsorships = append(view.Sponsorships, entry)
	}
	return view
}

// accessResolvers name who can resolve one missing authorization.
const (
	accessResolveOwnerAdmin = "owner_or_admin"
	accessResolveOperator   = "operator"
	accessResolveConnOwner  = "connection_owner"
	accessResolveRepoOwner  = "repository_owner"
	accessResolveMaintainer = "maintainer"
)

// accessActionView maps one missing authorization to the role that can
// resolve it and the settings section that owns the fix.
type accessActionView struct {
	Code      string `json:"code"`
	ResolveBy string `json:"resolve_by"`
	Setting   string `json:"setting"`
}

// accessRequiredActions renders the required owner actions behind an
// effective-authority verdict. Unknown codes stay visible with an empty
// resolver rather than vanishing or guessing.
func accessRequiredActions(missing []string) []accessActionView {
	actions := []accessActionView{}
	for _, code := range missing {
		action := accessActionView{Code: code}
		switch code {
		case factory.MissingPolicy, factory.MissingPolicyDisabled, factory.MissingPolicyPaused:
			action.ResolveBy, action.Setting = accessResolveOwnerAdmin, "policy"
		case factory.MissingOperatorGrant, factory.MissingOperatorWithdrawn, factory.MissingCapacity:
			action.ResolveBy, action.Setting = accessResolveOperator, "operator_grant"
			if code == factory.MissingCapacity {
				action.Setting = "capacity"
			}
		case factory.MissingSponsorship, factory.MissingSponsorshipGone:
			action.ResolveBy, action.Setting = accessResolveConnOwner, "sponsorships"
		case factory.MissingEnvironment, factory.MissingEnvironmentGone:
			action.ResolveBy, action.Setting = accessResolveRepoOwner, "environment_grant"
		case factory.MissingPreparation:
			action.ResolveBy, action.Setting = accessResolveOwnerAdmin, "project"
		case factory.MissingDispatch:
			action.ResolveBy, action.Setting = accessResolveMaintainer, "lifecycle"
		}
		actions = append(actions, action)
	}
	return actions
}

// accessUsageStatuses name why one connection's usage numbers are
// withheld instead of rendered.
const (
	accessUsageOwnerOnly    = "connection_owner_only"
	accessUsageUnavailable  = "usage_unavailable"
	accessUsageIdentityDown = "identity_unavailable"
)

// accessUsageView renders actual execution usage for one sponsored
// connection in real minutes against the sponsorship's allowance, or the
// explicit status withholding it. Allowance and spend belong to the
// connection owner; other viewers see the withholding status, never zeros
// that could read as no spend.
type accessUsageView struct {
	UsedMinutes   *int   `json:"used_minutes,omitempty"`
	Connection    string `json:"connection"`
	Unit          string `json:"unit"`
	Status        string `json:"status,omitempty"`
	BudgetMinutes int64  `json:"budget_minutes,omitempty"`
}

type accessUsageInput struct {
	Connection string
	Owned      bool
	UsedOK     bool
	Budget     int64
	Used       int
}

// accessUsageDTO renders one connection's usage. Values render only for
// the verified connection owner with the usage read succeeding; every
// other case names its withholding status.
func accessUsageDTO(in accessUsageInput) accessUsageView {
	view := accessUsageView{Connection: in.Connection, Unit: "minutes"}
	if !in.Owned {
		view.Status = accessUsageOwnerOnly
		return view
	}
	if !in.UsedOK {
		view.Status = accessUsageUnavailable
		return view
	}
	used := in.Used
	view.BudgetMinutes, view.UsedMinutes = in.Budget, &used
	return view
}

// RepositoryAccessView is the permission, readiness and usage read for one
// repository's Factory settings: the viewer's standing and save powers,
// the effective-authority verdict with required actions, preparation
// readiness and actual per-connection execution usage.
type RepositoryAccessView struct {
	Viewer       RepositoryViewer           `json:"viewer"`
	Editable     accessEditableView         `json:"editable"`
	Effective    factory.EffectiveAuthority `json:"effective"`
	Preparation  *RepositoryPreparation     `json:"preparation"`
	RequiredActs []accessActionView         `json:"required_actions"`
	Usage        []accessUsageView          `json:"usage"`
	Repository   string                     `json:"repository"`
	ObservedUnix int64                      `json:"observed_unix"`
}
