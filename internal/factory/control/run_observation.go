package control

import (
	"context"
	"errors"
	"reflect"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/factory/execution"
	"github.com/levitateos/sodaos/internal/identity"
	"github.com/levitateos/sodaos/internal/store"
)

// retainRunFacts starts from the latest retained identity, never the dispatch
// snapshot from before the runtime call. Custody is optional because a failed
// independent read must not discard a process already observed by the runtime.
func retainRunFacts(ctx context.Context, db *store.Store, run *factory.Run, state execution.RunObservation, custody *execution.CustodyObservation) error {
	current, err := db.FactoryRun(ctx, run.ID)
	if err != nil {
		return err
	}
	if current.Reconciled {
		return execution.ErrRunChanged
	}
	prior := current
	if custody == nil {
		err = adoptRunObservation(&current, state)
	} else {
		err = adoptExecutionFacts(&current, state, *custody)
	}
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(prior, current) {
		if err := db.SaveFactoryRun(ctx, current); err != nil {
			return err
		}
	}
	*run = current
	return nil
}

// observeRun records both boundaries without implying settlement. Typed absence
// before any execution facts were observed is allowed for an asynchronous launch;
// it is never retirement proof. Other unreadable custody stays fenced.
func observeRun(ctx context.Context, db *store.Store, broker execution.CustodyObserver, run *factory.Run, state execution.RunObservation) error {
	if err := retainRunFacts(ctx, db, run, state, nil); err != nil {
		return err
	}
	if broker == nil {
		return errors.New("execution custody observer unavailable")
	}
	custody, err := broker.ReadCustody(ctx, execution.RunAddress{Workspace: run.ProjectID, Run: run.ID})
	if errors.Is(err, execution.ErrCustodyNotObserved) && !run.HasExecutionIdentity() && !state.CredentialDeliveryObserved() && !state.CredentialReturnObserved() {
		return nil
	}
	if err != nil {
		return err
	}
	if err := retainCustodyFacts(ctx, db, run, custody); err != nil {
		return err
	}
	return retainRunFacts(ctx, db, run, state, &custody)
}

// A broker may still know acquisition after the process owner becomes
// unreachable. Keep that independent evidence before attempting a joint read;
// no process retirement or credential return follows from attribution alone.
func retainCustodyFacts(ctx context.Context, db *store.Store, run *factory.Run, custody execution.CustodyObservation) error {
	current, err := db.FactoryRun(ctx, run.ID)
	if err != nil {
		return err
	}
	if current.Reconciled || custody.ValidateFor(execution.RunAddress{Workspace: current.ProjectID, Run: current.ID}) != nil || custody.Kind != current.RuntimeKind() {
		return execution.ErrObservation
	}
	prior := current
	if current.Native != nil {
		n := *current.Native
		n.Provenance.Custody, n.Sealed = *custody.Native, custody.Sealed
		if !n.Advances(*current.Native) {
			return execution.ErrRunChanged
		}
		current.Native = &n
	} else {
		b := custody.Appliance
		if b.LeaseID != "" {
			if current.IdentityLeaseID != "" && current.IdentityLeaseID != b.LeaseID {
				return execution.ErrRunChanged
			}
			current.IdentityLeaseID = b.LeaseID
		}
		if b.Binding != nil {
			v := b.Binding
			binding := identity.Binding{Kind: v.Kind, ID: v.ID, Project: v.Boundary, Generation: v.Generation, InvocationID: v.Invocation, UID: v.UID, GID: v.GID, Login: v.Login, Scope: v.Scope, ChildID: v.ChildID, CredentialRoot: v.CredentialRoot}
			if current.IdentityBinding != nil && *current.IdentityBinding != binding || current.IdentityGeneration != 0 && current.IdentityGeneration != binding.Generation {
				return execution.ErrRunChanged
			}
			current.IdentityBinding, current.IdentityGeneration, current.CredentialDelegated = &binding, binding.Generation, true
		}
	}
	if err := current.Validate(); err != nil {
		return err
	}
	if !reflect.DeepEqual(prior, current) {
		if err := db.SaveFactoryRun(ctx, current); err != nil {
			return err
		}
	}
	*run = current
	return nil
}
