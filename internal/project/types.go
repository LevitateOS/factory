package project

import (
	"errors"
)

// Create is the project creation identity. The JSON shape is the Unix-socket
// wire contract with the privileged host daemon; field names are frozen.
type Create struct {
	Profile *Profile `json:"profile,omitempty"`
	ID      string   `json:"id"`
	Owner   int64    `json:"owner"`
}

func (c Create) Validate() error {
	if !ValidID(c.ID) || c.Owner <= 0 || c.Profile == nil || c.Profile.Validate() != nil {
		return errors.New("invalid creation identity")
	}
	return nil
}

// Account is a project login identity with its authorized keys.
type Account struct {
	Project  string   `json:"project"`
	Login    string   `json:"login"`
	Identity int64    `json:"identity"`
	Keys     []string `json:"keys"`
}

// ProjectAccessRequest asks the native Project helper to observe whether the
// bound account currently has administrator privileges.
type ProjectAccessRequest struct {
	Project  string `json:"project"`
	Login    string `json:"login"`
	Identity int64  `json:"identity"`
}

func (r ProjectAccessRequest) Validate() error {
	if !ValidID(r.Project) || !ValidLogin(r.Login) || r.Login == "root" || r.Identity <= 0 {
		return errors.New("invalid project privilege observation")
	}
	return nil
}

// ProjectAccessStatus is a native observation bound to one project account.
type ProjectAccessStatus struct {
	Project       string `json:"project"`
	Login         string `json:"login"`
	Identity      int64  `json:"identity"`
	Administrator *bool  `json:"administrator"`
}

// Environment is the observed live project instance: the project identity
// plus its running container attachment. It is not the creation profile.
type Environment struct {
	Image   string   `json:"image,omitempty"`
	Profile *Profile `json:"profile,omitempty"`
	ID      string   `json:"id"`
	IP      string   `json:"ip"`
	Running bool     `json:"running"`
}

// Connection is an environment plus its fixed public host key material.
type Connection struct {
	Environment Environment `json:"environment"`
	HostKey     string      `json:"host_key"`
	Fingerprint string      `json:"fingerprint"`
}

// Lifecycle is a start/stop/inspect request for one project.
type Lifecycle struct {
	Project string `json:"project"`
	Action  string `json:"action"`
}

// LifecycleState is the observed lifecycle outcome for one project.
type LifecycleState struct {
	Environment Environment `json:"environment"`
	BootEnabled bool        `json:"boot_enabled"`
}

// AccessKeys replaces or observes a login's authorized key set.
type AccessKeys struct {
	Project  string   `json:"project"`
	Login    string   `json:"login"`
	Identity int64    `json:"identity"`
	Revision string   `json:"revision,omitempty"`
	Keys     []string `json:"keys,omitempty"`
	Apply    bool     `json:"apply"`
}

// AccessKeyState is the observed key set and its revision.
type AccessKeyState struct {
	Revision string   `json:"revision"`
	Keys     []string `json:"keys"`
}

// OSRelease is an observation of the mutable root, never a creation profile.
type OSRelease struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Name    string `json:"name"`
}

// OSObservation is an environment plus its observed OS release.
type OSObservation struct {
	Environment Environment `json:"environment"`
	Release     *OSRelease  `json:"os_release"`
	Unavailable bool        `json:"os_release_unavailable"`
}
