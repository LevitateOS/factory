package project

import "errors"

// MemberAccess binds one explicit access operation to a retained Linux identity.
type MemberAccess struct {
	Project  string `json:"project"`
	Login    string `json:"login"`
	Identity int64  `json:"identity"`
	Action   string `json:"action"`
	Revision int64  `json:"revision"`
}

func (r MemberAccess) Validate() error {
	if !ValidID(r.Project) || !ValidLogin(r.Login) || r.Login == "root" || r.Identity <= 0 || r.Revision < 0 || (r.Action != "inspect" && r.Action != "revoke" && r.Action != "restore") {
		return errors.New("invalid member access operation")
	}
	return nil
}

type MemberProcess struct {
	PID     int    `json:"pid"`
	Command string `json:"command"`
}

// MemberAccessState reports native effects separately from the admission hold.
type MemberAccessState struct {
	Project          string          `json:"project"`
	Login            string          `json:"login"`
	Identity         int64           `json:"identity"`
	Revision         int64           `json:"revision"`
	Held             bool            `json:"held"`
	RequiresJoin     bool            `json:"requires_join"`
	LoginDisabled    bool            `json:"login_disabled"`
	KeysRemoved      bool            `json:"keys_removed"`
	ProcessesStopped bool            `json:"processes_stopped"`
	Keys             []string        `json:"keys"`
	Processes        []MemberProcess `json:"processes"`
}
