// Fixed factory-takeover identities and wire records. This file is pure
// validation: no I/O, no SQL, no privilege. Takeover copies a reconciled
// run's retained work into the admitted member's own checkout destination;
// the destination is derived, never caller-supplied, and provider homes
// plus role Git configuration are excluded from the copy.
package project

import (
	"errors"
)

// TakeoverDirName is the fixed directory inside a member home holding
// factory-takeover checkouts, one per run identity.
const TakeoverDirName = "factory-takeover"

// FactoryTakeover addresses one reconciled run and the admitted member
// receiving its retained work. The host derives the destination from
// these identities and confirms the recorded container incarnation.
type FactoryTakeover struct {
	Project string `json:"project"`
	ID      string `json:"id"`
	Member  string `json:"member"`
}

func (p FactoryTakeover) Validate() error {
	if !ValidID(p.Project) || !ValidFactoryRunID(p.ID) {
		return errors.New("invalid factory takeover address")
	}
	if !ValidLogin(p.Member) || p.Member == "root" {
		return errors.New("invalid takeover member")
	}
	return nil
}

// TakeoverDestination derives the member-owned checkout destination for one
// run. Empty reports an invalid identity; no caller-supplied path is
// ever accepted.
func TakeoverDestination(member, run string) string {
	if !ValidLogin(member) || member == "root" || !ValidFactoryRunID(run) {
		return ""
	}
	return "/home/" + member + "/" + TakeoverDirName + "/" + run
}

// TakeoverResult is the confirmed takeover outcome for one run and member.
type TakeoverResult struct {
	ID          string `json:"id"`
	Project     string `json:"project"`
	Member      string `json:"member"`
	Destination string `json:"destination"`
	Reused      bool   `json:"reused"`
}

func (r TakeoverResult) Validate() error {
	if !ValidID(r.Project) || !ValidFactoryRunID(r.ID) {
		return errors.New("invalid takeover result identity")
	}
	if TakeoverDestination(r.Member, r.ID) != r.Destination {
		return errors.New("takeover destination does not match its identities")
	}
	return nil
}
