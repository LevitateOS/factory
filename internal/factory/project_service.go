package factory

import "errors"

// ErrNotFound reports an unrecorded Factory run or Project. Reconciliation
// never adopts native work it did not record.
var ErrNotFound = errors.New("factory run not found")

// ProjectServiceRequest carries already admitted Forge operations over the
// private, OS-peer-gated Factory control socket. It grants no browser authority.
type ProjectServiceRequest struct {
	Action       string            `json:"action"`
	Project      string            `json:"project,omitempty"`
	Principal    string            `json:"principal,omitempty"`
	Repository   int64             `json:"repository,omitempty"`
	Verification StartVerification `json:"verification"`
}

type ProjectServiceResult struct {
	Withdrawal   Withdrawal         `json:"withdrawal"`
	Runs         []RunStopOutcome   `json:"runs"`
	Verification StartVerification  `json:"verification"`
	Authority    EffectiveAuthority `json:"authority"`
	DispatchOpen bool               `json:"dispatch_open"`
}
