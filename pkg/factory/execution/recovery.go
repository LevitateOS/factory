package execution

import (
	"context"
	"errors"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/project"
)

var ErrRecovery = errors.New("invalid execution work recovery binding")

type NativeWorkspaceRef struct {
	Profile    collaboration.ProfileID `json:"profile"`
	InstanceID string                  `json:"instance_id"`
	WorktreeID string                  `json:"worktree_id"`
}

type NativeCheckoutRef struct {
	Profile    collaboration.ProfileID `json:"profile"`
	CheckoutID string                  `json:"checkout_id"`
}

type NativeRecovery struct {
	Source NativeWorkspaceRef `json:"source"`
	Target NativeCheckoutRef  `json:"target"`
}

// RecoveryTarget selects a registered native checkout or an admitted appliance
// member. It supplies no arbitrary destination path or spending authority.
type RecoveryTarget struct {
	Member string
	Native *NativeCheckoutRef
}

func (t RecoveryTarget) Key() string {
	if t.Native != nil {
		return "native/" + string(t.Native.Profile) + "/" + t.Native.CheckoutID
	}
	return t.Member
}

type WorkRecoveryRequest struct {
	Address RunAddress
	Member  string
	Native  *NativeRecovery
}

func (r WorkRecoveryRequest) Target() RecoveryTarget {
	if r.Native != nil {
		return RecoveryTarget{Native: &r.Native.Target}
	}
	return RecoveryTarget{Member: r.Member}
}

func (r WorkRecoveryRequest) Validate() error {
	if r.Address.Validate() != nil || !project.ValidFactoryRunID(r.Address.Run) {
		return ErrRecovery
	}
	if r.Native == nil {
		if !project.ValidID(r.Address.Workspace) || !project.ValidLogin(r.Member) || r.Member == "root" {
			return ErrRecovery
		}
	} else {
		n := r.Native
		if r.Member != "" || !collaboration.ValidID(string(n.Source.Profile)) || !collaboration.ValidID(n.Source.InstanceID) || !collaboration.ValidText(n.Source.WorktreeID, 128) || n.Target.Profile != n.Source.Profile || !collaboration.ValidID(n.Target.CheckoutID) {
			return ErrRecovery
		}
	}
	return nil
}

func (r WorkRecoveryRequest) Equal(other WorkRecoveryRequest) bool {
	return r.Address == other.Address && r.Member == other.Member && (r.Native == nil && other.Native == nil || r.Native != nil && other.Native != nil && *r.Native == *other.Native)
}

// The native result addresses the registered checkout by reference. Appliance
// destinations retain their fixed member-home path, derived by the host.
type WorkRecoveryReceipt struct {
	Request     WorkRecoveryRequest
	Destination string
	Reused      bool
}

func (r WorkRecoveryReceipt) ValidateFor(request WorkRecoveryRequest) error {
	if request.Validate() != nil || !r.Request.Equal(request) {
		return ErrRecovery
	}
	if request.Native != nil {
		if r.Destination != "" {
			return ErrRecovery
		}
	} else if r.Destination != project.TakeoverDestination(request.Member, request.Address.Run) {
		return ErrRecovery
	}
	return nil
}

type WorkRecoverer interface {
	RecoverWork(context.Context, WorkRecoveryRequest) (WorkRecoveryReceipt, error)
}
