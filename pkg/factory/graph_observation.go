package factory

import "github.com/levitateos/factory/pkg/collaboration"

// GeneratedIssueObservation is the exact issue/edge evidence consumed during
// graph adoption. The adapter brackets and validates the provider read; this is
// not the complete provider snapshot or the browser collaboration read model.
type GeneratedIssueObservation struct {
	Issue        *GeneratedIssueFacts
	Dependencies *GeneratedDependencies
	// AuthorityRevision brackets the selected account separately when issues
	// have independent observation revisions rather than a global graph clock.
	AuthorityRevision int64
}

type GeneratedIssueFacts struct {
	ID               string
	Index            string
	Title            string
	Content          string
	TitleDigest      string
	ContentDigest    string
	ContentVersion   int
	Creator          *collaboration.ActorID
	CreatedUnix      int64
	CreationVerified bool
	FirstCreated     bool
	TitleOccurrence  string
	IsClosed         bool
	IsLocked         bool
	IsPull           bool
	Visible          bool
	Complete         bool
}

type GeneratedDependencies struct {
	IssueID  string
	Items    []GeneratedDependency
	Complete bool
}

type GeneratedDependency struct {
	OccurrenceID string
	DependencyID string
	Visible      bool
	Complete     bool
}

// CreationRecoveryBatch is a retained, sequence-bearing original-creation
// scan. End means the retained end, never complete historical coverage. These
// facts do not grant execution or assert current acceptance. Providers without
// original author/permission evidence must not fabricate this source from polls.
// Numeric positions and original issue IDs retain the existing journal's domain;
// mixed-provider identity translation remains the separate T02-IDENTITIES gate.
type CreationRecoveryBatch struct {
	Repository             int64
	Events                 []OriginalCreationEvent
	NextPosition           int64
	OldestRetainedPosition int64
	ReachedRetainedEnd     bool
	ProvenanceComplete     bool
	RetentionLimited       bool
}

type OriginalCreationEvent struct {
	Position        int64
	Delivery        string
	Repository      int64
	Issue           int64
	OriginalIssueID int64
	Creator         int64
	CreatorWrite    bool
	CreatedUnix     int64
	TitleDigest     string
	ContentDigest   string
	Verified        bool
}
