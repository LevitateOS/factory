package factory

// MCPFeedMessage is an assignment-visible communication or human reply. Inbox
// contains concise summaries; discussion contains full stored bodies/content.
// Discussion also carries the exact bounded authorized source snapshot and
// the contribution assessment behind an invitation, so the invited worker
// can read the original result/context and its reply target without a
// separate context helper.
type MCPFeedMessage struct {
	ID            string              `json:"id"`
	ParentID      string              `json:"parent_id,omitempty"`
	Actor         string              `json:"actor"`
	Author        string              `json:"author"`
	At            int64               `json:"at"`
	Status        string              `json:"status"`
	Summary       string              `json:"summary"`
	Body          string              `json:"body,omitempty"`
	Communication *FeedCommunication  `json:"communication,omitempty"`
	Consultation  *FeedConsultation   `json:"consultation,omitempty"`
	Source        *PostSource         `json:"source,omitempty"`
	Contribution  *ContributionReview `json:"contribution,omitempty"`
	Omitted       []string            `json:"omitted,omitempty"`
}

type MCPFeedPage struct {
	Items      []MCPFeedMessage `json:"items"`
	NextCursor string           `json:"next_cursor,omitempty"`
	Incomplete bool             `json:"incomplete"`
}
