// Assignment records bind one automatic coding dispatch to its eligible
// issue: the queued readiness it answered, the acceptance and authority it
// bound, the exact source/harness/preparation it launched, the prompt bytes
// it staged, and the result it recorded. Assigned work holds a resource
// reservation and a live run identity; finished work is terminal and keeps
// its result for inspection. These records never carry credentials: prompts
// embed accepted native text plus IDs and digests only.
package factory

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"

	"github.com/levitateos/sodaos/internal/project"
)

// Assignment stages. Assigned work holds its reservation and may still
// launch (bounded attempts); finished work is terminal and immutable.
const (
	AssignmentAssigned = "assigned"
	AssignmentFinished = "finished"
)

// MaxDispatchAttempts bounds launch tries per assignment. A launch that
// fails with confirmed-unused capacity releases its reservation and may
// try again; an exhausted assignment finishes failed instead of retrying
// forever.
const MaxDispatchAttempts = 3

// Assignment finish reasons. Bounded codes: views expose why an
// assignment finished, never the evidence bytes behind it.
const (
	AssignReasonReported   = "harness_reported"
	AssignReasonNoReport   = "harness_no_report"
	AssignReasonRunFailed  = "run_failed"
	AssignReasonCancelled  = "run_cancelled"
	AssignReasonExhausted  = "launch_exhausted"
	AssignReasonWithdrawn  = "dispatch_withdrawn"
	AssignReasonSuperseded = "acceptance_superseded"
)

func validAssignReason(reason string) bool {
	switch reason {
	case AssignReasonReported, AssignReasonNoReport, AssignReasonRunFailed,
		AssignReasonCancelled, AssignReasonExhausted, AssignReasonWithdrawn,
		AssignReasonSuperseded:
		return true
	default:
		return false
	}
}

// Subject kinds admitted for execution. Exactly one payload matches.
const (
	SubjectIssue  = "issue"
	SubjectIntent = "intent"
	// SubjectCompletion names one verified completion's reassessment cycle
	// (D13). It accepts only the reassess phase and the coder role.
	SubjectCompletion = "completion"
	// SubjectStall names one stall investigation cycle (D15). It accepts
	// only the repair phase and the coder role.
	SubjectStall = "stall"
	// SubjectDelivery names one accepted intent version's app verification
	// (D16). It accepts only the verify phase and the reviewer role.
	SubjectDelivery = "delivery"
	// SubjectDeliveryRepair names one failed verification's repair cycle
	// (D16). It accepts only the repair phase and the coder role.
	SubjectDeliveryRepair = "delivery_repair"
)

// IssueSubject binds one native issue attempt to the acceptance decision
// that authorized it.
type IssueSubject struct {
	Issue      int64  `json:"issue,string"`
	Acceptance string `json:"acceptance"`
}

// IntentSubject binds one intent planning attempt to its exact reviewed
// inputs. The owner is validated from the authoritative row at admission,
// never accepted from JSON.
type IntentSubject struct {
	IntentID    string `json:"intent_id"`
	RevisionID  string `json:"revision_id"`
	ScopeDigest string `json:"scope_digest"`
	OwnerID     int64  `json:"owner_id,string"`
}

// CompletionSubject binds one reassessment attempt to its verified
// completion and bounded cycle: the completion event, the immutable cycle
// and the sealed inventory digest the report must account for.
type CompletionSubject struct {
	CompletionID    string `json:"completion_id"`
	CycleID         string `json:"cycle_id"`
	InventoryDigest string `json:"inventory_digest"`
}

// ExecutionSubject is the canonical admitted work item: a native issue, a
// pre-issue intent draft, a D13 verified completion, a D15 stall, a D16
// delivery or a D16 delivery repair. Subject keys are
// `issue:<repository>:<issue>`, `intent:<intent_id>`,
// `completion:<completion_id>`, `stall:<stall_id>`,
// `delivery:<delivery_id>`,
// `delivery_repair:<delivery_id>:<failed_run>`.
type ExecutionSubject struct {
	Issue          *IssueSubject          `json:"issue,omitempty"`
	Intent         *IntentSubject         `json:"intent,omitempty"`
	Completion     *CompletionSubject     `json:"completion,omitempty"`
	Stall          *StallSubject          `json:"stall,omitempty"`
	Delivery       *DeliverySubject       `json:"delivery,omitempty"`
	DeliveryRepair *DeliveryRepairSubject `json:"delivery_repair,omitempty"`
	Kind           string                 `json:"kind"`
}

// SubjectKey returns the canonical admission key for subject.
func (s ExecutionSubject) SubjectKey(repository int64) string {
	if s.Kind == SubjectIntent && s.Intent != nil {
		return "intent:" + s.Intent.IntentID
	}
	if s.Kind == SubjectIssue && s.Issue != nil {
		return "issue:" + strconv.FormatInt(repository, 10) + ":" + strconv.FormatInt(s.Issue.Issue, 10)
	}
	if s.Kind == SubjectCompletion && s.Completion != nil {
		return "completion:" + s.Completion.CompletionID
	}
	if s.Kind == SubjectStall && s.Stall != nil {
		return StallKey(s.Stall.StallID)
	}
	if s.Kind == SubjectDelivery && s.Delivery != nil {
		return DeliveryKey(s.Delivery.DeliveryID)
	}
	if s.Kind == SubjectDeliveryRepair && s.DeliveryRepair != nil {
		return DeliveryRepairKey(s.DeliveryRepair.DeliveryID, s.DeliveryRepair.FailedVerificationRunID)
	}
	return ""
}

// ValidCollectionID reports whether id names a library collection:
// lowercase plan-key shape so sponsorship collection grants match exactly.
func ValidCollectionID(id string) bool { return ValidPlanKey(id) }

func (s ExecutionSubject) Validate() error {
	switch s.Kind {
	case SubjectIssue:
		return checkIssueSubject(s)
	case SubjectIntent:
		return checkIntentSubject(s)
	case SubjectCompletion:
		return checkCompletionSubject(s)
	case SubjectStall:
		return checkStallSubject(s)
	case SubjectDelivery:
		return checkDeliverySubject(s)
	case SubjectDeliveryRepair:
		return checkDeliveryRepairSubject(s)
	default:
		return errors.New("unknown execution subject kind")
	}
}

func checkIssueSubject(s ExecutionSubject) error {
	if s.Issue == nil || s.Intent != nil || s.Completion != nil ||
		s.Stall != nil || s.Delivery != nil || s.DeliveryRepair != nil {
		return errors.New("issue subject needs exactly its issue payload")
	}
	if s.Issue.Issue <= 0 || !project.ValidDecisionID(s.Issue.Acceptance) {
		return errors.New("invalid issue subject")
	}
	return nil
}

func checkIntentSubject(s ExecutionSubject) error {
	if s.Intent == nil || s.Issue != nil || s.Completion != nil ||
		s.Stall != nil || s.Delivery != nil || s.DeliveryRepair != nil {
		return errors.New("intent subject needs exactly its intent payload")
	}
	if !project.ValidDecisionID(s.Intent.IntentID) || !project.ValidDecisionID(s.Intent.RevisionID) {
		return errors.New("invalid intent subject identity")
	}
	if !ValidDigest(s.Intent.ScopeDigest) || s.Intent.OwnerID <= 0 {
		return errors.New("invalid intent subject scope or owner")
	}
	return nil
}

func checkCompletionSubject(s ExecutionSubject) error {
	if s.Completion == nil || s.Issue != nil || s.Intent != nil ||
		s.Stall != nil || s.Delivery != nil || s.DeliveryRepair != nil {
		return errors.New("completion subject needs exactly its completion payload")
	}
	if !ValidID(s.Completion.CompletionID) || !ValidID(s.Completion.CycleID) {
		return errors.New("invalid completion subject identity")
	}
	if !ValidDigest(s.Completion.InventoryDigest) {
		return errors.New("invalid completion subject inventory digest")
	}
	return nil
}

func checkStallSubject(s ExecutionSubject) error {
	if s.Stall == nil || s.Issue != nil || s.Intent != nil ||
		s.Completion != nil || s.Delivery != nil || s.DeliveryRepair != nil {
		return errors.New("stall subject needs exactly its stall payload")
	}
	return s.Stall.Validate()
}

func checkDeliverySubject(s ExecutionSubject) error {
	if s.Delivery == nil || s.Issue != nil || s.Intent != nil ||
		s.Completion != nil || s.Stall != nil || s.DeliveryRepair != nil {
		return errors.New("delivery subject needs exactly its delivery payload")
	}
	return s.Delivery.Validate()
}

func checkDeliveryRepairSubject(s ExecutionSubject) error {
	if s.DeliveryRepair == nil || s.Issue != nil || s.Intent != nil ||
		s.Completion != nil || s.Stall != nil || s.Delivery != nil {
		return errors.New("delivery repair subject needs exactly its delivery repair payload")
	}
	return s.DeliveryRepair.Validate()
}

// Assignment is one durable automatic dispatch for an admitted subject. Run
// is the latest run identity; RunHistory lists every attempt's run identity
// in order. Result arrives at finish; the prompt bytes stay inspectable
// afterwards. ActorID binds the broker actor from the selected sponsorship
// grant to this assignment. Phase names the admitted execution phase and
// ParentAssignment the immediate parent; a root has no parent and keeps its
// own AttemptRoot. A D13 reassessment cycle is always a fresh root with no
// parent and no publication assignment. Admission always records Subject,
// Phase and ParentAssignment.
type Assignment struct {
	AcceptedContext       *AcceptedContext          `json:"accepted_context,omitempty"`
	ReviewContext         *CurrentReviewContext     `json:"review_context,omitempty"`
	PlanningContext       *SemanticPlannerInput     `json:"planning_context,omitempty"`
	Authority             AuthorityRef              `json:"authority"`
	Result                *AssignmentResult         `json:"result,omitempty"`
	Subject               ExecutionSubject          `json:"subject,omitempty"`
	Prompt                []byte                    `json:"prompt"`
	RunHistory            []string                  `json:"run_history"`
	ID                    string                    `json:"id"`
	AttemptRoot           string                    `json:"attempt_root"`
	ParentAssignment      string                    `json:"parent_assignment,omitempty"`
	PublicationAssignment string                    `json:"publication_assignment"`
	ProjectID             string                    `json:"project_id"`
	Role                  string                    `json:"role"`
	Phase                 string                    `json:"phase,omitempty"`
	Acceptance            string                    `json:"acceptance"`
	Preparation           string                    `json:"preparation"`
	Harness               string                    `json:"harness"`
	HarnessVers           string                    `json:"harness_version"`
	AdoptedHarness        project.FactoryHarnessPin `json:"adopted_harness,omitempty"`
	Model                 string                    `json:"model"`
	Connection            string                    `json:"connection"`
	SourceCommit          string                    `json:"source_commit"`
	PromptSHA             string                    `json:"prompt_sha"`
	Run                   string                    `json:"run"`
	Stage                 string                    `json:"stage"`
	Outcome               Outcome                   `json:"outcome,omitempty"`
	Reason                string                    `json:"reason,omitempty"`
	Repository            int64                     `json:"repository,string"`
	Issue                 int64                     `json:"issue,string"`
	Revision              int64                     `json:"revision"`
	NativeRev             int64                     `json:"native_revision"`
	Attempts              int                       `json:"attempts"`
	CreatedUnix           int64                     `json:"created_unix"`
	FinishedUnix          int64                     `json:"finished_unix,omitempty"`
	ActorID               int64                     `json:"actor_id,string"`
}

// validateSubjectPhase enforces the D01 admission invariants for assignments
// carrying an explicit subject.
func (a Assignment) validateSubjectPhase() error {
	if err := a.Subject.Validate(); err != nil {
		return err
	}
	if !project.ValidExecutionPhase(a.Phase) || a.Phase == "" {
		return errors.New("subject assignment lacks its execution phase")
	}
	if !SubjectKindAllowed(a.Subject.Kind, a.Phase) {
		return errors.New("subject kind cannot enter its execution phase")
	}
	if !ValidPhaseRole(a.Phase, a.Role) {
		return errors.New("assignment role does not match its execution phase")
	}
	if err := checkAssignmentAttemptRoot(a); err != nil {
		return err
	}
	if err := checkSubjectKindScope(a); err != nil {
		return err
	}
	if a.Subject.Kind == SubjectDelivery && a.Subject.Delivery.SourceCommit != a.SourceCommit {
		return errors.New("delivery assignment runs a head its subject was not admitted against")
	}
	return nil
}

func checkAssignmentAttemptRoot(a Assignment) error {
	if a.ParentAssignment == "" {
		if a.AttemptRoot != a.ID {
			return errors.New("root assignment must carry its own attempt root")
		}
		return nil
	}
	if !ValidID(a.ParentAssignment) || a.ParentAssignment == a.ID {
		return errors.New("invalid assignment parent")
	}
	if !ValidID(a.AttemptRoot) || a.AttemptRoot == a.ID {
		return errors.New("child assignment must keep its attempt root")
	}
	return nil
}

func checkSubjectKindScope(a Assignment) error {
	switch a.Subject.Kind {
	case SubjectIssue:
		return checkIssueAssignmentScope(a)
	case SubjectIntent:
		return checkIntentAssignmentScope(a)
	case SubjectCompletion:
		return checkCompletionAssignmentScope(a)
	case SubjectStall, SubjectDelivery, SubjectDeliveryRepair:
		return checkRepairAssignmentScope(a)
	}
	return nil
}

func checkIssueAssignmentScope(a Assignment) error {
	if a.Repository <= 0 || a.Issue <= 0 || a.Issue != a.Subject.Issue.Issue {
		return errors.New("issue assignment does not match its subject")
	}
	if a.Acceptance != a.Subject.Issue.Acceptance {
		return errors.New("issue assignment acceptance does not match its subject")
	}
	return nil
}

func checkIntentAssignmentScope(a Assignment) error {
	// A private intent decompose assignment is a FRESH root (D01):
	// no parent, own attempt root, and no publication assignment
	// until implementation. Both self and other publication IDs
	// refuse; no child intent lives under another root.
	if a.ParentAssignment != "" || a.AttemptRoot != a.ID {
		return errors.New("intent assignment is a fresh attempt root")
	}
	if a.PublicationAssignment != "" {
		return errors.New("intent assignment carries no publication")
	}
	// An intent draft is not a native issue: never record zero as a
	// fake issue number, and no acceptance decision exists before
	// planning. The repository slot is shared with issue attempts.
	if a.Issue != 0 || a.Repository <= 0 || a.Acceptance != "" {
		return errors.New("intent assignment carries issue authority")
	}
	return nil
}

func checkCompletionAssignmentScope(a Assignment) error {
	// A reassessment cycle is not an issue attempt and never publishes:
	// a fresh attempt root with no parent or publication assignment, in
	// the completed repository slot, carrying no issue authority. It
	// reviews the sealed inventory but implements nothing.
	if a.ParentAssignment != "" || a.AttemptRoot != a.ID {
		return errors.New("reassessment cycle is a fresh attempt root")
	}
	if a.PublicationAssignment != "" {
		return errors.New("reassessment cycle carries no publication")
	}
	if a.Issue != 0 || a.Repository <= 0 || a.Acceptance != "" {
		return errors.New("completion assignment carries issue authority")
	}
	return nil
}

func checkRepairAssignmentScope(a Assignment) error {
	// A stall investigation, app verification or delivery repair is
	// not an issue attempt and never publishes: a fresh attempt root
	// with no parent or publication assignment, in the repository
	// slot, carrying no issue authority. Repair runs single-agent
	// with no children; verification runs read-only. Their admission
	// validators (R37/R38) own episode/version binding; this gate
	// pins the root, slot and publication shape.
	if a.ParentAssignment != "" || a.AttemptRoot != a.ID {
		return errors.New("repair cycle is a fresh attempt root")
	}
	if a.PublicationAssignment != "" {
		return errors.New("repair cycle carries no publication")
	}
	if a.Issue != 0 || a.Repository <= 0 || a.Acceptance != "" {
		return errors.New("repair assignment carries issue authority")
	}
	return nil
}

// assignmentPromptBound returns the admitted prompt size for assignment:
// the 320 KiB exception when the recorded phase and subject match
// (reassess with completion, repair with stall or delivery repair), the
// 128 KiB intent-decompose headroom for decompose with an intent subject,
// the standard transport bound otherwise. Binding both pins each
// exception to its genuine cycle: decompose admits only intent subjects.
func assignmentPromptBound(a Assignment) int {
	if a.Phase == project.ExecutionReassess && a.Subject.Kind == SubjectCompletion && a.Subject.Completion != nil {
		return project.MaxReassessPrompt
	}
	if a.Phase == project.ExecutionRepair &&
		(a.Subject.Kind == SubjectStall && a.Subject.Stall != nil ||
			a.Subject.Kind == SubjectDeliveryRepair && a.Subject.DeliveryRepair != nil) {
		return project.MaxReassessPrompt
	}
	if a.Phase == project.ExecutionDecompose && a.Subject.Kind == SubjectIntent && a.Subject.Intent != nil {
		return project.MaxDecomposePrompt
	}
	return project.MaxFactoryPrompt
}

// Validate rejects malformed assignments. Assigned work carries no outcome
// or result; finished work carries both plus its reason.
func (a Assignment) Validate() error {
	if a.ReviewContext != nil {
		if err := a.ReviewContext.ValidateFor(a); err != nil {
			return err
		}
	}
	if a.AcceptedContext != nil {
		if err := a.AcceptedContext.ValidateFor(a); err != nil {
			return err
		}
	}
	if a.PlanningContext != nil {
		if err := a.PlanningContext.ValidateAssignment(a); err != nil {
			return err
		}
	}
	if !ValidID(a.ID) || !ValidID(a.AttemptRoot) {
		return errors.New("invalid assignment identity")
	}
	// A reassessment cycle never publishes, so it carries no publication
	// assignment; a private intent root likewise carries no publication
	// until implementation (D01); stall, delivery and delivery-repair
	// cycles never publish either (D15/D16). Every other assignment names
	// its publication owner; complete subject validation follows in
	// validateSubjectPhase.
	if err := checkAssignmentPublication(a); err != nil {
		return err
	}
	if !ValidProjectID(a.ProjectID) || !project.ValidFactoryRole(a.Role) {
		return errors.New("invalid assignment address")
	}
	if a.Subject.Kind == "" {
		return errors.New("subject assignment lacks its subject kind")
	}
	if err := a.validateSubjectPhase(); err != nil {
		return err
	}
	if err := checkAssignmentExecution(a); err != nil {
		return err
	}
	if err := checkAssignmentRunHistory(a); err != nil {
		return err
	}
	return checkAssignmentStage(a)
}

func checkAssignmentPublication(a Assignment) error {
	switch a.Subject.Kind {
	case SubjectCompletion, SubjectStall, SubjectDelivery, SubjectDeliveryRepair:
		if a.PublicationAssignment != "" {
			return errors.New("non-issue cycle carries no publication")
		}
	case SubjectIntent:
		// Narrow D01 exemption: defer to validateSubjectPhase, which
		// enforces empty publication plus fresh-root intent identity.
	default:
		if !ValidID(a.PublicationAssignment) {
			return errors.New("invalid assignment identity")
		}
	}
	return nil
}

func checkAssignmentExecution(a Assignment) error {
	if a.Revision < 0 || a.NativeRev < 1 {
		return errors.New("invalid assignment scope")
	}
	if a.ActorID <= 0 {
		return errors.New("invalid assignment execution actor")
	}
	if !ValidPreparationRef(a.Preparation) {
		return errors.New("invalid assignment preparation")
	}
	if a.Harness == "" || len(a.Harness) > 128 || !project.ValidHarnessVersion(a.HarnessVers) {
		return errors.New("invalid assignment harness")
	}
	// Recorded proof is optional for rows admitted before tool-identity
	// proof existed; when present it must be a fully valid pin.
	if a.AdoptedHarness != (project.FactoryHarnessPin{}) {
		if err := a.AdoptedHarness.Validate(); err != nil {
			return errors.New("invalid assignment adopted harness: " + err.Error())
		}
	}
	if a.Model == "" || len(a.Model) > 128 {
		return errors.New("invalid assignment model")
	}
	if a.Connection == "" || len(a.Connection) > 128 {
		return errors.New("invalid assignment connection")
	}
	if !ValidCommit(a.SourceCommit) {
		return errors.New("invalid assignment source")
	}
	if len(a.Prompt) == 0 || len(a.Prompt) > assignmentPromptBound(a) {
		return errors.New("invalid assignment prompt size")
	}
	if sum := sha256.Sum256(a.Prompt); hex.EncodeToString(sum[:]) != a.PromptSHA {
		return errors.New("assignment prompt does not match its digest")
	}
	if a.Attempts < 1 || a.Attempts > MaxDispatchAttempts {
		return errors.New("invalid assignment attempt count")
	}
	return nil
}

func checkAssignmentRunHistory(a Assignment) error {
	if !ValidID(a.Run) || len(a.RunHistory) == 0 || len(a.RunHistory) > MaxDispatchAttempts {
		return errors.New("invalid assignment run history")
	}
	seen := make(map[string]bool, len(a.RunHistory))
	for _, run := range a.RunHistory {
		if !ValidID(run) || seen[run] {
			return errors.New("invalid assignment run history")
		}
		seen[run] = true
	}
	if a.RunHistory[len(a.RunHistory)-1] != a.Run {
		return errors.New("assignment run is not its latest attempt")
	}
	if len(a.RunHistory) != a.Attempts {
		return errors.New("assignment attempts do not match its run history")
	}
	return nil
}

func checkAssignmentStage(a Assignment) error {
	switch a.Stage {
	case AssignmentAssigned:
		if a.Outcome != "" || a.Reason != "" || a.Result != nil || a.FinishedUnix != 0 {
			return errors.New("assigned work carries no outcome")
		}
	case AssignmentFinished:
		return checkFinishedAssignmentResult(a)
	default:
		return errors.New("invalid assignment stage")
	}
	return nil
}

func checkFinishedAssignmentResult(a Assignment) error {
	if !validOutcome(a.Outcome) || a.Outcome == "" || !validAssignReason(a.Reason) || a.FinishedUnix <= 0 {
		return errors.New("finished assignment lacks its outcome")
	}
	if a.Result == nil || a.Result.ValidateForAssignment(a.Role, a.SourceCommit) != nil {
		return errors.New("finished assignment lacks its recorded result")
	}
	if a.Result.AssignmentID != a.ID || a.Result.RunID != a.Run {
		return errors.New("assignment result does not match its attempt")
	}
	if a.Result.PhaseResult != nil {
		if err := a.Result.ValidatePhaseBinding(a.Role, a.Phase); err != nil {
			return errors.New("finished assignment lacks its bound phase result")
		}
	}
	return nil
}

// ValidPreparationRef reports whether id is an admissible preparation
// reference for dispatch records.
func ValidPreparationRef(id string) bool {
	return project.ValidPreparationID(id)
}
