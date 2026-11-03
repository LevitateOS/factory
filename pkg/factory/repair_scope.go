package factory

const (
	RepairObservationPageSize      = 8
	MaxRepairObservations          = 32
	RepairObservationFragmentBytes = 4096
)

// RepairScopeView lists this investigator's durable intermediate reports.
// The existing required-current context supplies the sealed cause, complete
// inventory and remaining allowance through its bounded continuation pages.
type RepairScopeView struct {
	Assignment      string              `json:"assignment"`
	Run             string              `json:"run"`
	InputDigest     string              `json:"input_digest"`
	Subject         string              `json:"subject"`
	SourceCommit    string              `json:"source_commit"`
	InventoryDigest string              `json:"inventory_digest"`
	Records         []RepairScopeRecord `json:"records"`
	Total           int                 `json:"total"`
	Limit           int                 `json:"limit"`
	MaximumPerTool  int                 `json:"maximum_per_tool"`
	NextAfter       int64               `json:"next_after,omitempty"`
	Complete        bool                `json:"complete"`
}

type RepairScopeRecord struct {
	ID       string  `json:"id"`
	Tool     MCPTool `json:"tool"`
	Sequence int64   `json:"sequence"`
}

// RepairObservationPage reads a post's complete report in bounded UTF-8
// fragments. Concatenate content by offset, then decode the JSON.
// This grants no terminal or adoption authority.
type RepairObservationPage struct {
	ID         string  `json:"id"`
	Tool       MCPTool `json:"tool"`
	Offset     int     `json:"offset"`
	NextOffset int     `json:"next_offset,omitempty"`
	TotalBytes int     `json:"total_bytes"`
	Encoding   string  `json:"encoding"`
	Content    string  `json:"content"`
	Complete   bool    `json:"complete"`
}

// RepairInventoryDigest selects only the complete cause admitted to this
// Strategist. No repository-wide or sibling inventory is a fallback.
func RepairInventoryDigest(current CurrentContext) string {
	switch {
	case current.Role.Strategy != nil && current.Role.Strategy.Complete:
		return current.Role.Strategy.InventoryDigest
	case current.Role.Repair != nil && current.Role.Repair.Complete:
		return current.Role.Repair.InventoryDigest
	case current.Role.DeliveryRepair != nil && current.Role.DeliveryRepair.Complete:
		return current.Role.DeliveryRepair.Subject.InventoryDigest
	default:
		return ""
	}
}
