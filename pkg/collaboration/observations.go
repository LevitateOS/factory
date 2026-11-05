package collaboration

import "time"

// Observed facts cross the trusted native connector port without credentials,
// canonical IDs, capabilities or workflow decisions. The Go registry maps them.
type ObservedActor struct {
	ExternalID string `json:"external_id"`
	Login      string `json:"login"`
}
type ObservedTeam struct {
	ExternalID string `json:"external_id"`
	Name       string `json:"name"`
}
type ObservedRepository struct {
	ExternalID string `json:"external_id"`
	Name       string `json:"name"`
}
type ObservedLabel struct {
	ExternalID string `json:"external_id"`
	Name       string `json:"name"`
	Color      string `json:"color"`
}
type ObservedIssue struct {
	ExternalID string          `json:"external_id"`
	Number     int64           `json:"number"`
	Title      string          `json:"title"`
	Body       string          `json:"body"`
	State      string          `json:"state"`
	Locked     *bool           `json:"locked"`
	Author     *ObservedActor  `json:"author"`
	Assignees  []ObservedActor `json:"assignees"`
	Labels     []ObservedLabel `json:"labels"`
	CreatedAt  *time.Time      `json:"created_at"`
	UpdatedAt  *time.Time      `json:"updated_at"`
	ClosedAt   *time.Time      `json:"closed_at"`
}
type ObservedIssuePage struct {
	Repository string          `json:"repository"`
	Name       string          `json:"name"`
	Items      []ObservedIssue `json:"items"`
	NextPage   int             `json:"next_page"`
}

// ConnectionObservationBatch is applied as one Go-owned SQL transaction.
// Repository, when set, must already belong to this host and must match the
// sole newly observed repository. Resolve-only batches contain no new facts.
type ConnectionObservationBatch struct {
	Connection   ConnectionObservation
	Repository   RepositoryID
	Repositories []ObservedRepository
	Actors       []ObservedActor
	Teams        []ObservedTeam
	// References are provider-observed repository identities mentioned by the
	// resource (for example a PR fork). Recording them never associates the
	// connection with access to those repositories.
	References []ObservedRepository
}
type ConnectionObservationMapping struct {
	Binding      ConnectionBinding
	Repository   *RepositoryRef
	Repositories []RepositoryRef
	Actors       map[string]ActorRef
	Teams        map[string]TeamRef
	References   map[string]RepositoryRef
}
