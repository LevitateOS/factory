package factory

import (
	"encoding/json"

	"github.com/levitateos/sodaos/internal/factory/execution"
)

// CurrentContext is the capability-bound, read-only projection of canonical
// required inputs. Optional bundle selection never changes these fields.
// Nil denotes unavailable material; an empty collection denotes observed empty.
type CurrentContext struct {
	Version   string                   `json:"version"`
	Binding   CurrentContextBinding    `json:"binding"`
	Intent    CurrentContextIntent     `json:"intent"`
	Run       *CurrentContextRun       `json:"run"`
	Allowance *CurrentContextAllowance `json:"allowance"`
	Evidence  CurrentContextEvidence   `json:"evidence"`
	Role      CurrentRoleContext       `json:"role"`
}

type CurrentContextBinding struct {
	RunID                 string `json:"run_id"`
	Project               string `json:"project"`
	PreparationDigest     string `json:"preparation_digest"`
	AcceptedRevision      string `json:"accepted_revision"`
	AttemptRoot           string `json:"attempt_root"`
	ParentAssignment      string `json:"parent_assignment,omitempty"`
	PublicationAssignment string `json:"publication_assignment,omitempty"`
	AssignmentRevision    int64  `json:"assignment_revision"`
	Stage                 string `json:"stage"`
}

type CurrentContextIntent struct {
	AcceptanceID  string `json:"acceptance_id"`
	ContextDigest string `json:"context_digest"`
	ObjectiveRef  string `json:"objective_ref"`
	OutcomeCount  *int   `json:"outcome_count"`
}

type CurrentContextRun struct {
	ID            string  `json:"id"`
	Outcome       Outcome `json:"outcome"`
	Reconciled    bool    `json:"reconciled"`
	Active        bool    `json:"active"`
	Settled       bool    `json:"settled"`
	ObservedState string  `json:"observed_state"`
}

type CurrentContextAllowance struct {
	Revision                  int64 `json:"revision"`
	DeadlineRemainingSeconds  int64 `json:"deadline_remaining_seconds"`
	ActiveSecondsRemaining    int64 `json:"active_seconds_remaining"`
	CorrectionCyclesRemaining *int  `json:"correction_cycles_remaining"`
	ReviewRoundsRemaining     *int  `json:"review_rounds_remaining"`
	MaxChildren               int   `json:"max_children"`
	Closed                    bool  `json:"closed"`
}

type CurrentContextEvidence struct {
	RequiredStreams []string `json:"required_streams"`
	Availability    string   `json:"availability"`
	MissingReasons  []string `json:"missing_reasons"`
}

// CurrentRoleContext contains only registered, explicitly projected inputs.
// New producers can fill their own role header without widening a parent grant.
type CurrentRoleContext struct {
	Tools          []MCPTool                     `json:"tools,omitempty"`
	ResultTool     MCPTool                       `json:"result_tool,omitempty"`
	ResultSchema   json.RawMessage               `json:"result_schema,omitempty"`
	Review         *CurrentReviewContext         `json:"review,omitempty"`
	RecordSource   string                        `json:"record_source,omitempty"`
	Accepted       *AcceptedContext              `json:"accepted,omitempty"`
	Kind           MCPProfile                    `json:"kind"`
	Plan           *CurrentPlanContext           `json:"plan,omitempty"`
	Product        *CurrentProductContext        `json:"product,omitempty"`
	Planning       *CurrentPlanningContext       `json:"planning,omitempty"`
	Verification   *CurrentVerificationEvidence  `json:"verification,omitempty"`
	Strategy       *CurrentStrategyContext       `json:"strategy,omitempty"`
	Repair         *CurrentRepairContext         `json:"repair,omitempty"`
	DeliveryRepair *CurrentDeliveryRepairContext `json:"delivery_repair,omitempty"`
	Delivery       *DeliverySubject              `json:"delivery,omitempty"`
	Acceptance     *CurrentAcceptanceContext     `json:"acceptance,omitempty"`
}

type CurrentPlanContext struct {
	AttemptRoot               string                       `json:"attempt_root"`
	PlanID                    string                       `json:"plan_id"`
	Generation                int                          `json:"generation"`
	Digest                    string                       `json:"digest"`
	InputDigest               string                       `json:"input_digest"`
	ScopeDigest               string                       `json:"scope_digest"`
	SourceCommit              string                       `json:"source_commit"`
	IntegrationCommit         string                       `json:"integration_commit"`
	VerifiedIntegrationCommit *string                      `json:"verified_integration_commit"`
	FinalCheckpoint           *execution.CheckpointReceipt `json:"final_checkpoint"`
	Preparation               string                       `json:"preparation"`
	ManifestDigest            string                       `json:"manifest_digest"`
	CurrentPlanHead           *CurrentPlanHead             `json:"current_plan_head"`
	Tasks                     []PlanTask                   `json:"tasks"`
	Checks                    []CheckSpec                  `json:"checks"`
	Receipts                  []CurrentTaskReceipt         `json:"task_receipts"`
}

type CurrentPlanHead struct {
	PlanID     string `json:"plan_id"`
	Generation int64  `json:"generation"`
}

// CurrentTaskReceipt intentionally excludes event logs and actor metadata.
type CurrentTaskReceipt struct {
	AttemptRoot  string   `json:"attempt_root"`
	PlanID       string   `json:"plan_id"`
	TaskID       string   `json:"task_id"`
	Revision     int64    `json:"revision"`
	State        string   `json:"state"`
	Child        string   `json:"child,omitempty"`
	InputDigest  string   `json:"input_digest,omitempty"`
	OutputDigest string   `json:"output_digest,omitempty"`
	Tree         string   `json:"tree,omitempty"`
	Commit       string   `json:"commit,omitempty"`
	Checks       []string `json:"checks"`
	Reason       string   `json:"reason,omitempty"`
}

// CurrentProductContext is copied from one exact authorized accepted revision;
// private owners and idea actor identities never enter this projection.
type CurrentProductContext struct {
	Intent            string               `json:"intent"`
	AcceptedRevision  string               `json:"accepted_revision"`
	ScopeDigest       string               `json:"scope_digest"`
	Objective         string               `json:"objective"`
	Approach          string               `json:"approach,omitempty"`
	Ideas             []CurrentProductIdea `json:"ideas"`
	Tasks             []IntentTask         `json:"tasks"`
	EffectiveOutcomes []string             `json:"effective_outcomes"`
}

type CurrentProductIdea struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Consequence string `json:"consequence"`
	Assumption  bool   `json:"assumption"`
	Decision    string `json:"decision"`
	Correction  string `json:"correction,omitempty"`
	Source      string `json:"source,omitempty"`
	Provenance  string `json:"provenance,omitempty"`
	Evidence    string `json:"evidence,omitempty"`
	Material    bool   `json:"material,omitempty"`
}

// CurrentAcceptanceContext names exact accepted source revisions and native
// prerequisite receipts. Their text remains unavailable until a scoped producer
// supplies it; digests never masquerade as complete objective text.
type CurrentAcceptanceContext struct {
	ID             string                 `json:"id"`
	NativeRevision int64                  `json:"native_revision"`
	TitleDigest    string                 `json:"title_digest"`
	ContentDigest  string                 `json:"content_digest"`
	Sources        []SelectedSource       `json:"sources"`
	Prerequisites  []AcceptedPrerequisite `json:"prerequisites"`
	Resolutions    []SelectedSource       `json:"resolutions"`
}
