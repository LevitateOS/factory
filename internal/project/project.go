// Package project owns the canonical Soda project domain: creation identity,
// lifecycle states, access keys and OS observations shared by storage, the
// dashboard HTTP layer, the host Unix client and privileged execution. It
// performs no I/O, no HTTP and no privileged operations; validation here is
// pure. Every other package references these types directly instead of
// maintaining parallel DTOs.
package project

import (
	"regexp"
)

var (
	projectID   = regexp.MustCompile(`^p[0-9a-f]{24}$`)
	loginName   = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,30}$`)
	imageID     = regexp.MustCompile(`^(?:sha256:)?[0-9a-f]{64}$`)
	containerID = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// ValidID reports whether id is a well-formed project identity.
func ValidID(id string) bool { return projectID.MatchString(id) }

// ValidLogin reports whether login is an admissible project login name.
func ValidLogin(login string) bool { return loginName.MatchString(login) }

// ValidImageRef reports whether ref is an admissible image digest reference.
func ValidImageRef(ref string) bool { return imageID.MatchString(ref) }

// ValidContainerID reports whether id is a well-formed container identity.
func ValidContainerID(id string) bool { return containerID.MatchString(id) }
