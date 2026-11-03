package identity

import "time"

// Event retains non-secret attribution after a live lease is removed.
type Event struct {
	ID           int64     `json:"id,string"`
	Time         time.Time `json:"time"`
	Action       string    `json:"action"`
	OwnerID      int64     `json:"owner_id,string"`
	ActorID      int64     `json:"actor_id,string"`
	ConnectionID string    `json:"connection_id"`
	LeaseID      string    `json:"lease_id,omitempty"`
	ProjectID    string    `json:"project_id,omitempty"`
	GrantID      string    `json:"grant_id,omitempty"`
	ExecutionID  string    `json:"execution_id,omitempty"`
	Kind         string    `json:"kind,omitempty"`
	Generation   int64     `json:"generation"`
}
