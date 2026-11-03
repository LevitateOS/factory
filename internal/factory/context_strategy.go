package factory

// CurrentStrategyContext is the complete sealed producer input granted by one
// completion cycle. Mutable collector/adoption state and private journals are
// deliberately outside this model-facing projection.
type CurrentStrategyContext struct {
	CompletionID     string                    `json:"completion_id"`
	CycleID          string                    `json:"cycle_id"`
	Generation       int64                     `json:"generation"`
	InventoryDigest  string                    `json:"inventory_digest"`
	EvidenceRevision int64                     `json:"evidence_revision"`
	Local            CompletionLocal           `json:"local"`
	Issues           []CompletionIssueEvidence `json:"issues"`
	Complete         bool                      `json:"complete"`
}
