package project

import "errors"

// These verdicts follow a successful native export invocation. Transport
// failures remain errors, never evidence that the candidate is invalid.
var (
	ErrFactoryExportCandidate = errors.New("export candidate is not recorded")
	ErrFactoryExportBounds    = errors.New("candidate export exceeds bounds")
)

// FactoryExport addresses one settled run's exact candidate for bundle
// export: the run identity, its recorded role and preparation, and the
// controller-selected reported candidate. The host verifies the run,
// role, preparation and container incarnation against its receipt, then
// exports the exact commit. The controller owns the assignment/result
// binding; the publisher independently validates the bundle and policy.
type FactoryExport struct {
	Project     string `json:"project"`
	ID          string `json:"id"`
	Role        string `json:"role"`
	Preparation string `json:"preparation"`
	Candidate   string `json:"candidate"`
}

func (p FactoryExport) Validate() error {
	if !ValidID(p.Project) || !ValidFactoryRunID(p.ID) {
		return errors.New("invalid factory export address")
	}
	if !ValidFactoryRole(p.Role) {
		return errors.New("invalid factory export role")
	}
	if !ValidPreparationID(p.Preparation) {
		return errors.New("invalid factory export preparation")
	}
	if !ValidCommit(p.Candidate) {
		return errors.New("invalid factory export candidate")
	}
	return nil
}

// FactoryExportState is one observed candidate export: the run identity
// with its exact candidate and phase, plus the base64-encoded bounded
// Git bundle. Only Git objects and HEAD are exported; role configuration
// and untracked run files are not copied.
type FactoryExportState struct {
	ID        string `json:"id"`
	Project   string `json:"project"`
	Phase     string `json:"phase"`
	Container string `json:"container"`
	Candidate string `json:"candidate"`
	Bundle    string `json:"bundle"`
}
