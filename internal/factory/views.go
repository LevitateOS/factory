// RunView binds one recorded factory run to its repository, issue and
// attempt for Spaces rendering. It is a display binding only: it grants no
// authority, admits no intake and schedules nothing. A fixture may record the
// binding for a proof run; the binding never substitutes for native viewer
// authority or the run/process identity it renders. Full acceptance and
// attempt records belong to later factory tasks.
package factory

import (
	"errors"
	"regexp"
)

var viewAttempt = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// ValidRunViewAttempt reports whether attempt is an admissible opaque view
// label. Empty means the run is not attributed to a named attempt.
func ValidRunViewAttempt(attempt string) bool {
	return attempt == "" || viewAttempt.MatchString(attempt)
}

// RunView is the minimal durable issue/attempt binding for one run. Issue 0
// means the run is not attributed to a native issue.
type RunView struct {
	RunID      string `json:"run_id"`
	Repository int64  `json:"repository"`
	Issue      int64  `json:"issue,omitempty"`
	Attempt    string `json:"attempt,omitempty"`
}

// Validate rejects malformed bindings. It does not check that the run,
// repository or issue exists; recording order enforces the run reference.
func (v RunView) Validate() error {
	if !ValidID(v.RunID) {
		return errors.New("invalid run view identity")
	}
	if v.Repository <= 0 || v.Issue < 0 {
		return errors.New("invalid run view scope")
	}
	if !ValidRunViewAttempt(v.Attempt) {
		return errors.New("invalid run view attempt")
	}
	return nil
}
