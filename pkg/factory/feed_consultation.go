package factory

// FeedConsultation is the display record of an actual worker System One call.
// Its funding snapshot remains the call identity. The request belongs to the
// consulting worker; Answer is recorded provider output, never agent prose.
type FeedConsultation struct {
	Snapshot       string         `json:"snapshot"`
	GroupID        string         `json:"group_id"`
	Assignment     string         `json:"assignment"`
	SourceRevision string         `json:"source_revision,omitempty"`
	Tool           string         `json:"tool"`
	Kind           string         `json:"kind"`
	State          any            `json:"state,omitempty"`
	Instructions   any            `json:"instructions,omitempty"`
	Criteria       any            `json:"criteria,omitempty"`
	Questions      map[string]any `json:"questions,omitempty"`
	Model          string         `json:"model,omitempty"`
	Status         string         `json:"status"`
	Answer         map[string]any `json:"answer,omitempty"`
	CreatedUnix    int64          `json:"created_unix"`
	UpdatedUnix    int64          `json:"updated_unix"`
}

func FeedConsultationPostID(snapshot string) string {
	return MCPInputDigest([]byte("systemone-consultation:" + snapshot))[:32]
}
