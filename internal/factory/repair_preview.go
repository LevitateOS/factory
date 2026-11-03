package factory

// RepairPreview is deterministic structural feedback, never a proposal judgment,
// recorded draft, graph application or promise of new readiness. Valid means
// the deterministic adoption preflight passes, not that a no-change diagnosis
// is an invalid report or that a proposal has received its required judgment.
type RepairPreview struct {
	Subject         string                        `json:"subject"`
	InputDigest     string                        `json:"input_digest"`
	SourceCommit    string                        `json:"source_commit"`
	InventoryDigest string                        `json:"inventory_digest"`
	Valid           bool                          `json:"valid"`
	Reason          string                        `json:"reason,omitempty"`
	Validation      *ReassessmentValidationReport `json:"validation,omitempty"`
	Effects         []RepairPreviewEffect         `json:"effects"`
	NextStep        string                        `json:"next_step"`
}

type RepairPreviewEffect struct {
	IntentID          string   `json:"intent_id"`
	SuccessorRevision string   `json:"successor_revision"`
	PacketDigest      string   `json:"packet_digest"`
	NoEffect          bool     `json:"no_effect"`
	Creates           int      `json:"creates"`
	Edits             int      `json:"edits"`
	DependencyChanges int      `json:"dependency_changes"`
	PreservedTasks    []string `json:"preserved_tasks"`
	ReboundTasks      []string `json:"rebound_tasks"`
	RetiredTasks      []string `json:"retired_tasks"`
}
