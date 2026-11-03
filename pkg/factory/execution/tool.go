package execution

import (
	"context"
	"strings"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/project"
)

// ToolObserver reads the runtime's current proved tool selection. It does not
// authorize dispatch or replace the executable captured by an existing run.
type ToolObserver interface {
	InspectTool(context.Context, string) (ToolObservation, error)
}

// ToolObservation keeps native executable identity distinct from an appliance
// bundle. A native executable supplies no container image or isolation claim.
type ToolObservation struct {
	Kind      collaboration.RuntimeKind
	Native    *NativeTool
	Appliance *ApplianceTool
}

type ApplianceTool struct {
	Harness string `json:"harness"`
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
	Image   string `json:"image"`
}

func (t ApplianceTool) Validate() error {
	image, pinned := strings.CutPrefix(t.Image, "sha256:")
	if !project.ValidHarnessFamily(t.Harness) || !project.ValidHarnessVersion(t.Version) || !validDigest(t.SHA256) || !pinned || !validDigest(image) {
		return ErrProvenance
	}
	return nil
}

func (o ToolObservation) ValidateFor(family string) error {
	if !project.ValidHarnessFamily(family) {
		return ErrProvenance
	}
	switch o.Kind {
	case collaboration.Native:
		if o.Native == nil || o.Appliance != nil || o.Native.Harness != family || o.Native.Validate() != nil {
			return ErrProvenance
		}
	case collaboration.Appliance:
		if o.Appliance == nil || o.Native != nil || o.Appliance.Harness != family || o.Appliance.Validate() != nil {
			return ErrProvenance
		}
	default:
		return ErrProvenance
	}
	return nil
}
