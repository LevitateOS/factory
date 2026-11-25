package factory

import (
	"errors"
	"slices"
	"strings"
)

// A post may name one Factory role (slices 5.3 and 5.6): an agent's ask names
// the role whose expertise it asks for (FeedCommunication.AddressedTo) and a
// person's post names the role invited to read it first
// (PostRequest.ReadFirstBy). Both are canonical MCPProfiles, never a person,
// login or account, and neither grants anything: the person still answers
// questions, JEV still routes and classifies posts, and a role only replies
// when the contribution assessment invites it. The named role is assessed
// first among the eligible running roles (store contributionRoles).

// ErrRoleReference refuses a name that is not one canonical Factory role.
var ErrRoleReference = errors.New(roleReferenceMessage())

func roleReferenceMessage() string {
	names := make([]string, 0, len(MCPProfiles))
	for _, profile := range MCPProfiles {
		names = append(names, string(profile))
	}
	return "a post names one Factory role (" + strings.Join(names, ", ") + "), never a person or login"
}

// CheckRoleReference is the one rule for a role named on a post.
func CheckRoleReference(role MCPProfile) error {
	if slices.Contains(MCPProfiles, role) {
		return nil
	}
	return ErrRoleReference
}
