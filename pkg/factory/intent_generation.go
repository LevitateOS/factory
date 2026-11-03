package factory

import "encoding/json"

// GeneratedIssueIdentity pins the existing generation domain's issue ID,
// repository-local index and content version. It conveys neither acceptance nor
// execution authority. The Delivered map uses these same identities to preserve
// previously closed history without mutation; closure alone is not merge proof.
type GeneratedIssueIdentity struct {
	IssueID        int64 `json:"issue_id,string"`
	IssueIndex     int64 `json:"issue_index,string"`
	ContentVersion int   `json:"content_version"`
}

// IntentGeneration records the immutable native mutation before submission.
// Native receipts and exact task acceptances retain distinct provenance.
// SubmittedTasks is the exact task graph rendered into Payload: the accepted
// revision tasks, or the S07-selected scoped decomposition of them. Adoption
// and later deltas bind this submitted graph, never a re-derived one.
// Delivered carries the subset bound to closed native history without
// mutation; those keys must stay absent from the native receipt.
type IntentGeneration struct {
	TaskIssueIDs   map[string]int64                  `json:"task_issue_ids,omitempty"`
	SubmittedTasks []IntentTask                      `json:"submitted_tasks"`
	Delivered      map[string]GeneratedIssueIdentity `json:"delivered,omitempty"`
	RevisionID     string                            `json:"revision_id"`
	IntentID       string                            `json:"intent_id"`
	OperationID    string                            `json:"operation_id"`
	ActorID        int64                             `json:"actor_id,string"`
	NativeRev      int64                             `json:"native_revision"`
	AuthRevision   string                            `json:"authorization_revision"`
	NotAfter       int64                             `json:"not_after"`
	Payload        json.RawMessage                   `json:"payload"`
	IntentDigest   string                            `json:"intent_digest"`
	Receipt        json.RawMessage                   `json:"receipt,omitempty"`
	State          string                            `json:"state"`
	// PlanAssignment is the recorded planning assignment this operation
	// was resolved from. It is immutable from creation; submission and
	// adoption refuse missing or stale provenance.
	PlanAssignment string `json:"plan_assignment,omitempty"`
	// VerificationManifest is the selected proposal bound to its approved
	// preparation. Execution requires its own trusted per-run consumer.
	VerificationManifest       *AdoptedVerificationManifest `json:"verification_manifest,omitempty"`
	VerificationManifestDigest string                       `json:"verification_manifest_digest,omitempty"`
	// ReassessmentCycle is the alternative recorded owner for an approved
	// serial reassessment delta. Retained bindings supply native identity
	// for unchanged issues absent from a mutation receipt; copied bindings
	// additionally retain their exact earlier acceptance without re-admission.
	ReassessmentCycle string `json:"reassessment_cycle,omitempty"`
	// RepairCycle names a genuine stall cycle using the same protected serial
	// graph owner. It never manufactures a completion event or coder ledger.
	RepairCycle      string              `json:"repair_cycle,omitempty"`
	RetainedBindings []IntentTaskBinding `json:"retained_bindings,omitempty"`
	CopiedBindings   []IntentTaskBinding `json:"copied_bindings,omitempty"`
	LocalOnly        bool                `json:"local_only,omitempty"`
}

// ProtectedGraphCycle names the distinct completion or stall owner. Ambiguous
// provenance returns empty and is refused by both activation boundaries.
func (g IntentGeneration) ProtectedGraphCycle() string {
	if g.ReassessmentCycle != "" && g.RepairCycle == "" {
		return g.ReassessmentCycle
	}
	if g.RepairCycle != "" && g.ReassessmentCycle == "" {
		return g.RepairCycle
	}
	return ""
}

type IntentTaskBinding struct {
	RevisionID     string `json:"revision_id"`
	TaskKey        string `json:"task_key"`
	IssueID        int64  `json:"issue_id,string"`
	IssueIndex     int64  `json:"issue_index,string"`
	ContentVersion int    `json:"content_version"`
	AcceptanceID   string `json:"acceptance_id"`
	CreatedUnix    int64  `json:"created_unix,omitempty"`
}
