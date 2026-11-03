package control

// D14 capability/submission admission (FW38).
//
// Every MCP request validates capability plus profile/tool/input
// binding, including tools absent from the caller's discovery set.
// Mutations repeat freshness/authority checks at admission.
// Caller-supplied role, child ID or environment variable proves
// nothing: the recorded capability row and the observed run state
// decide. Expiry, stop intent, withdrawal and terminal retirement
// revoke access.

import (
	"errors"
	"time"

	"github.com/levitateos/factory/pkg/factory"
)

var (
	// ErrMCPCapabilityInactive reports a call under a capability that
	// is not active: staged, revoked or retired.
	ErrMCPCapabilityInactive = errors.New("factory capability is not active: this run has ended or not started; stop calling tools and end the session")
	// ErrMCPCapabilityExpired reports a call past the recorded
	// deadline. Rate limits never extend deadlines.
	ErrMCPCapabilityExpired = errors.New("factory capability is expired: the run deadline passed; stop calling tools and end the session")
	// ErrMCPCapabilityStopped reports a call racing current stop
	// intent: activation compares the observed binding, and stop
	// ordered before start permanently prevents admission.
	ErrMCPCapabilityStopped = errors.New("factory run is stopped: the owner or SodaOS stopped this run; stop calling tools and end the session")
	// ErrMCPCapabilityBinding reports a profile/tool/input binding
	// the recorded capability does not authorize, including tools
	// the caller's discovery set never advertised.
	ErrMCPCapabilityBinding = errors.New("capability does not authorize this call: this role may not use this tool or input; call only the tools this server lists for the run")
)

// MCPRunObservation is the host-observed run state read outside SQL:
// run identity, incarnation, assignment, authority, deadline and stop
// state. CAS activation checks this observation against the packet
// and the recorded capability inside the transaction.
type MCPRunObservation struct {
	RunID        string
	Incarnation  uint64
	AssignmentID string
	Authority    string
	Stopped      bool
}

// AdmitMCPCapability gates one tool call on the recorded capability
// and the observed run state. now is the admission instant; the
// caller supplies the observed run/incarnation from outside SQL so a
// restarted or replaced run cannot borrow the old authority.
func AdmitMCPCapability(capability factory.MCPCapability, tool factory.MCPTool, now time.Time, observed MCPRunObservation) error {
	if err := capability.Validate(); err != nil {
		return err
	}
	if capability.State != factory.MCPCapabilityActive {
		return ErrMCPCapabilityInactive
	}
	if capability.Expired(now) {
		return ErrMCPCapabilityExpired
	}
	if observed.Stopped {
		return ErrMCPCapabilityStopped
	}
	if observed.RunID != "" && observed.RunID != capability.RunID {
		return ErrMCPCapabilityBinding
	}
	if observed.AssignmentID != "" && observed.AssignmentID != capability.AssignmentID {
		return ErrMCPCapabilityBinding
	}
	if observed.Authority != "" && observed.Authority != capability.Authority {
		return ErrMCPCapabilityBinding
	}
	if !factory.MCPToolAdmitted(capability.Profile, tool) {
		return ErrMCPCapabilityBinding
	}
	return nil
}
