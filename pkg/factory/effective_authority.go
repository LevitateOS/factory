package factory

// EvaluateAuthority derives the visible verdict from current records. Every
// grant is checked separately; a browser session, issue content or one
// grant cannot substitute for another.
func EvaluateAuthority(in AuthorityInput) EffectiveAuthority {
	var missing []string
	var ref AuthorityRef
	switch {
	case in.Policy == nil:
		missing = append(missing, MissingPolicy)
	case !in.Policy.Enabled:
		missing = append(missing, MissingPolicyDisabled)
		ref.Policy = in.Policy.Revision
	case in.Policy.Paused:
		missing = append(missing, MissingPolicyPaused)
		ref.Policy = in.Policy.Revision
	default:
		ref.Policy = in.Policy.Revision
	}
	switch {
	case in.Operator == nil:
		missing = append(missing, MissingOperatorGrant)
	case !in.Operator.Active:
		missing = append(missing, MissingOperatorWithdrawn)
		ref.Operator = in.Operator.Revision
	default:
		ref.Operator = in.Operator.Revision
	}
	if in.Appliance == nil {
		missing = append(missing, MissingCapacity)
	} else {
		ref.Capacity = in.Appliance.Revision
	}
	switch {
	case in.Sponsorship == nil:
		missing = append(missing, MissingSponsorship)
	case !in.Sponsorship.Active:
		missing = append(missing, MissingSponsorshipGone)
		ref.Sponsorship = in.Sponsorship.Revision
		ref.SponsorshipConnection = in.Sponsorship.Connection
	default:
		ref.Sponsorship = in.Sponsorship.Revision
		ref.SponsorshipConnection = in.Sponsorship.Connection
	}
	switch {
	case in.Environment == nil:
		missing = append(missing, MissingEnvironment)
	case !in.Environment.Active:
		missing = append(missing, MissingEnvironmentGone)
		ref.Environment = in.Environment.Revision
	default:
		ref.Environment = in.Environment.Revision
	}
	if in.ProjectExists && !in.PreparationReady {
		missing = append(missing, MissingPreparation)
	}
	if !in.DispatchOpen {
		missing = append(missing, MissingDispatch)
	}
	if missing == nil {
		missing = []string{}
	}
	return EffectiveAuthority{Missing: missing, Authority: ref, Effective: len(missing) == 0, DispatchOpen: in.DispatchOpen}
}
