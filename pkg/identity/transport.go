package identity

// Request is the private Unix HTTP protocol. Browser handlers never accept it.
type Request struct {
	ProviderID  string          `json:"provider_id"`
	OwnerID     int64           `json:"owner_id,string"`
	ID          string          `json:"id"`
	Label       string          `json:"label"`
	ProjectID   string          `json:"project_id"`
	Kind        string          `json:"kind,omitempty"`
	ExecutionID string          `json:"execution_id,omitempty"`
	Grant       *GrantRequest   `json:"grant,omitempty"`
	Acquire     *AcquireRequest `json:"acquire,omitempty"`
	Binding     *Binding        `json:"binding,omitempty"`
	Credential  []byte          `json:"credential,omitempty"`
}
