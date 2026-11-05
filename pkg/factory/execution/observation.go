package execution

import (
	"context"
	"errors"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/project"
)

var (
	ErrRunNotObserved     = errors.New("execution run not observed")
	ErrRunChanged         = errors.New("execution run incarnation changed")
	ErrCustodyNotObserved = errors.New("execution custody not observed")
	ErrObservation        = errors.New("invalid execution observation")
)

// RunAddress names an existing recorded run, not permission to start it. An
// adapter validates its own workspace namespace before calling its runtime.
type RunAddress struct {
	Run       string
	Workspace string
}

func (a RunAddress) Validate() error {
	if !collaboration.ValidText(a.Run, 128) || !collaboration.ValidText(a.Workspace, 128) {
		return ErrObservation
	}
	return nil
}

// RunObserver reads or retires exactly one identity. Missing observations are
// typed errors; neither absence nor a transport failure proves retirement.
type RunObserver interface {
	InspectRun(context.Context, RunAddress) (RunObservation, error)
	RetireRun(context.Context, RunAddress) (RunObservation, error)
}

// RunLauncher consumes semantic inputs with a captured runtime binding. Each
// adapter owns its transport and returns that runtime's actual observation.
type RunLauncher interface {
	LaunchRun(context.Context, LaunchRequest) (RunObservation, error)
}

// CustodyObserver concerns only this run's acquired credential material.
// SealCustody acknowledges sealing the identity, not release of credentials;
// callers must subsequently read and reconcile the actual custody observation.
type CustodyObserver interface {
	ReadCustody(context.Context, RunAddress) (CustodyObservation, error)
	SealCustody(context.Context, RunAddress) error
}

// RunObservation supplies common execution results plus exactly one runtime's
// facts. It is not the placement-bound Provenance record: historic runs do not
// yet have canonical runtime IDs or placement generations. Those cannot be
// invented by a translator. VerificationChecks is the existing shared manifest
// receipt, independently validated against the admitted run by its consumer.
type RunObservation struct {
	Run       string
	Workspace string
	Role      string
	Phase     string
	Output    string
	// OutputComplete is the native owner's retained output observation. A false
	// value is unknown or incomplete, never evidence of a successful isolated run.
	OutputComplete     bool
	Reason             string
	ExitCode           *int
	Live               bool
	Retirement         Retirement
	Kind               collaboration.RuntimeKind
	Appliance          *ApplianceRunFacts
	Native             *NativeProvenance
	VerificationChecks *project.FactoryVerificationChecksState
}

// ApplianceRunFacts retains observed boundary/lease facts without fabricating
// the image required by a complete ApplianceBoundary. That image belongs to
// the captured admission. Empty invocation fields stay empty on stop tombstones.
type ApplianceRunFacts struct {
	Invocation          ApplianceInvocation
	LeaseID             string
	CredentialDelivered bool
	CredentialReturned  bool
}

type CustodyObservation struct {
	Run string
	// Workspace stays unknown unless attested. An appliance broker binding
	// names the container, not the workspace; never fill this from a request.
	Workspace string
	// Sealed means this identity cannot acquire again. A sealed broker row
	// with retained lease/binding metadata does not prove credential return.
	Sealed    bool
	Kind      collaboration.RuntimeKind
	Appliance *BrokerObservation
	Native    *NativeCustody
}

func (o RunObservation) InvocationID() string {
	if o.Kind == collaboration.Appliance && o.Appliance != nil {
		return o.Appliance.Invocation.Invocation
	}
	if o.Kind == collaboration.Native && o.Native != nil && o.Native.Process != nil {
		return o.Native.Process.InvocationID
	}
	return ""
}

func (o RunObservation) CredentialDeliveryObserved() bool {
	if o.Kind == collaboration.Appliance && o.Appliance != nil {
		return o.Appliance.CredentialDelivered
	}
	return o.Kind == collaboration.Native && o.Native != nil && o.Native.Custody.AcquisitionID != "" &&
		(o.Native.Custody.State == Held || o.Native.Custody.State == Released)
}

func (o RunObservation) CredentialReturnObserved() bool {
	if o.Kind == collaboration.Appliance && o.Appliance != nil {
		return o.Appliance.CredentialReturned
	}
	return o.Kind == collaboration.Native && o.Native != nil && o.Native.Custody.State == Released
}

func (o RunObservation) ValidateFor(a RunAddress) error {
	if a.Validate() != nil || o.Run != a.Run || o.Workspace != a.Workspace ||
		!project.ValidFactoryPhase(o.Phase) || (o.Retirement != NotStarted && o.Retirement != Running && o.Retirement != Exited && o.Retirement != Retired && o.Retirement != Uncertain) ||
		(o.Live && o.Retirement == Retired) {
		return ErrObservation
	}
	switch o.Kind {
	case collaboration.Appliance:
		if o.Appliance == nil || o.Native != nil || o.Appliance.Invocation.Project != o.Workspace ||
			(o.Live && o.Appliance.Invocation.Invocation == "") {
			return ErrObservation
		}
	case collaboration.Native:
		if o.Native == nil || o.Appliance != nil || !validNative(*o.Native) || (o.Live && o.Native.Process == nil) {
			return ErrObservation
		}
	default:
		return ErrObservation
	}
	return nil
}

func (o CustodyObservation) ValidateFor(a RunAddress) error {
	if a.Validate() != nil || o.Run != a.Run || (o.Workspace != "" && o.Workspace != a.Workspace) {
		return ErrObservation
	}
	switch o.Kind {
	case collaboration.Appliance:
		if o.Appliance == nil || o.Native != nil || !validAppliance(ApplianceProvenance{Broker: *o.Appliance}, o.Run) {
			return ErrObservation
		}
		if b := o.Appliance.Binding; b != nil && b.ID != a.Run {
			return ErrObservation
		}
	case collaboration.Native:
		if o.Native == nil || o.Appliance != nil || !validNativeCustody(*o.Native) {
			return ErrObservation
		}
	default:
		return ErrObservation
	}
	return nil
}
