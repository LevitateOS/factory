package mcpserver

import (
	"context"
	"encoding/json"
	"time"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/control"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
	"github.com/levitateos/factory/pkg/project"
)

type SubmissionSink interface {
	TerminalMCPProposal(context.Context, string) (factory.MCPWorkStatus, bool, error)
	RecordMCPSubmission(ctx context.Context, submission factory.MCPSubmission) (factory.MCPWorkStatus, error)
}

// Prepare supplies current worker admission; Observed supplies
// runtime state for per-tool admission. Catalog reads the authenticated
// capability's approved preparation; nil keeps the catalog unavailable.
type Dependencies struct {
	IssueMaker func(context.Context, factory.MCPCapability, factory.MCPTool, map[string]any, string) (map[string]any, error)
	// boundCapability is supplied by the authenticated transport, never tool
	// arguments. Its immutable scope supplies identity and default revisions.
	boundCapability      *factory.MCPCapability
	sequentialTasks      bool
	limiter              *mcpRateLimiter
	Prepare              func(context.Context, factory.MCPCapability) (factory.MCPCapability, error)
	Capabilities         factoryservice.WorkerCapabilitySource
	Submissions          SubmissionSink
	Assignments          factoryservice.WorkerAssignmentSource
	PriorityJudgments    factoryservice.WorkerPriorityJudgmentSource
	CurrentContext       factoryservice.WorkerCurrentContextSource
	CheckpointReceipts   factoryservice.WorkerCheckpointSource
	ConsultationJournal  factoryservice.WorkerConsultationJournal
	Artifacts            ArtifactWriter
	Library              factoryservice.LibrarySource
	Discussions          DiscussionSource
	Posts                PostWriter
	Reassessment         ReassessmentScopeSource
	RepairObservations   RepairObservationSource
	VerificationJourneys VerificationJourneySource
	Runs                 factoryservice.WorkerRunSource
	Observed             func(capability factory.MCPCapability) (control.MCPRunObservation, error)
	Catalog              func(context.Context, factory.MCPCapability) (factory.ApprovedCatalog, error)
	Now                  func() time.Time
	SystemOne            factoryservice.WorkerSystemOne
	VerificationEvidence func(context.Context, factory.MCPCapability) (map[string]any, error)
	RepairPreview        func(context.Context, factory.MCPCapability, json.RawMessage) (factory.RepairPreview, error)
	NativeTask           func(context.Context, factory.MCPCapability, string, string) (map[string]any, error)
	ReadSource           func(context.Context, factory.MCPCapability, project.FactoryContextReadPage) (map[string]any, error)
	ListSource           func(context.Context, factory.MCPCapability, string) (map[string]any, error)
}

// Existing hosts supplied a multi-purpose Submissions object. Resolve those
// optional interfaces once at public composition; tool handlers consume explicit
// ports. Explicit ports win and retain the same current source object.
func completeLegacyDependencies(deps Dependencies) Dependencies {
	// Consultation journals historically came only from the explicit priority
	// source, independently of the context reader's terminal-source fallback.
	if deps.ConsultationJournal == nil {
		deps.ConsultationJournal, _ = deps.PriorityJudgments.(factoryservice.WorkerConsultationJournal)
	}
	if deps.PriorityJudgments == nil {
		deps.PriorityJudgments, _ = deps.Submissions.(factoryservice.WorkerPriorityJudgmentSource)
	}
	if deps.CurrentContext == nil {
		deps.CurrentContext, _ = deps.Submissions.(factoryservice.WorkerCurrentContextSource)
	}
	if deps.CheckpointReceipts == nil {
		deps.CheckpointReceipts, _ = deps.Submissions.(factoryservice.WorkerCheckpointSource)
	}
	if deps.Artifacts == nil {
		deps.Artifacts, _ = deps.Submissions.(ArtifactWriter)
	}
	if deps.Library == nil {
		deps.Library, _ = deps.Submissions.(factoryservice.LibrarySource)
	}
	if deps.Discussions == nil {
		deps.Discussions, _ = deps.Submissions.(DiscussionSource)
	}
	if deps.Posts == nil {
		deps.Posts, _ = deps.Submissions.(PostWriter)
	}
	if deps.Reassessment == nil {
		deps.Reassessment, _ = deps.Submissions.(ReassessmentScopeSource)
	}
	if deps.RepairObservations == nil {
		deps.RepairObservations, _ = deps.Submissions.(RepairObservationSource)
	}
	if deps.VerificationJourneys == nil {
		deps.VerificationJourneys, _ = deps.Submissions.(VerificationJourneySource)
	}
	return deps
}
