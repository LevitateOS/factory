package control

import (
	"context"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/execution"
	"github.com/levitateos/factory/pkg/project"
	"github.com/levitateos/factory/pkg/store"
)

// Common prompt metadata retains its tagged proof. A native executable has no
// appliance image or appliance pin, including while constructing an assignment.
type dispatchTool struct {
	Harness, Version, SHA256, Image string
	proof                           execution.ToolObservation
}

func captureDispatchTool(o execution.ToolObservation) dispatchTool {
	if o.Native != nil {
		n := *o.Native
		o.Native = &n
		return dispatchTool{Harness: n.Harness, Version: n.Version, SHA256: n.SHA256, proof: o}
	}
	a := *o.Appliance
	o.Appliance = &a
	return dispatchTool{Harness: a.Harness, Version: a.Version, SHA256: a.SHA256, Image: a.Image, proof: o}
}

func (t dispatchTool) appliancePin() project.FactoryHarnessPin {
	if t.proof.Appliance == nil {
		return project.FactoryHarnessPin{}
	}
	return project.FactoryHarnessPin{Harness: t.Harness, Version: t.Version, SHA256: t.SHA256, Image: t.Image}
}

func (p *attemptPlan) captureRuntime(a *factory.Assignment, run *factory.Run) *execution.NativeLaunch {
	if p.tool.proof.Native == nil {
		return nil
	}
	n := execution.NativeLaunch{Workspace: *p.prep.Native, Tool: *p.tool.proof.Native, BundleSHA256: p.prep.State.SourceBundleSHA256,
		PromptSHA256: a.PromptSHA, Runtime: p.nativeEnvironment.Runtime, PlacementGeneration: p.nativeEnvironment.Generation,
		TrustMode: "isolated_system_worker", BrokerRun: run.ID}
	if p.sponsorship.Native != nil {
		n.CredentialSHA256 = p.sponsorship.Native.CredentialSHA256
	}
	a.NativeTool = &n.Tool
	run.Native = &execution.NativeRecord{Tool: n.Tool, BundleSHA256: n.BundleSHA256, PromptSHA256: n.PromptSHA256, Retirement: execution.NotStarted,
		Provenance: execution.NativeProvenance{TrustMode: n.TrustMode, Profile: n.Workspace.Profile, InstanceID: n.Workspace.InstanceID, WorktreeID: n.Workspace.WorktreeID, BrokerRun: n.BrokerRun, Custody: execution.NativeCustody{State: execution.NotAcquired}}}
	return &n
}

func (p *attemptPlan) recordPacket(ctx context.Context, db *store.Store, a factory.Assignment, reservation factory.Reservation, run factory.Run, view factory.RunView) error {
	native := p.captureRuntime(&a, &run)
	if native != nil {
		return db.RecordNativeDispatchPacket(ctx, p.control, a, reservation, run, view, *native)
	}
	return db.RecordDispatchPacket(ctx, p.control, a, reservation, run, view)
}
