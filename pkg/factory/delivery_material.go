package factory

import (
	"errors"
	"strconv"
	"time"
)

// DeliveryNativeMaterial is complete bounded, permission-checked PR discussion
// paired with the accepted input observation. Revision is not a global provider
// revision; the complete discussion is independently content-bound. Callers
// never ask a semantic judge to infer a review from an identifier or hash.
// Repository and participant actor keys are canonical Factory identities.
// PR/issue/review/comment identities, commit anchors and content/conversation
// digests remain provider facts. Revision stays outside the semantic fingerprint.
type DeliveryNativeMaterial struct {
	Revision          int64                  `json:"native_revision"`
	PolicyRevision    int64                  `json:"policy_revision,omitempty"`
	Repository        int64                  `json:"repository,string"`
	PRID              int64                  `json:"pr_id,string"`
	IssueID           int64                  `json:"issue_id,string"`
	Head              string                 `json:"head"`
	Base              string                 `json:"base"`
	Title             string                 `json:"title"`
	Body              string                 `json:"body"`
	Comments          []DeliveryComment      `json:"comments"`
	Reviews           []DeliveryNativeReview `json:"reviews"`
	Threads           []DeliveryThread       `json:"threads"`
	Eligible          bool                   `json:"eligible"`
	Blocker           string                 `json:"blocker"`
	BlockingReviewIDs []string               `json:"blocking_review_ids"`
	ReviewDecision    *string                `json:"review_decision,omitempty"`
}

type DeliveryComment struct {
	ID             string     `json:"id"`
	PosterID       string     `json:"poster_id"`
	ReviewID       string     `json:"review_id"`
	Type           string     `json:"type"`
	Content        string     `json:"content"`
	Digest         string     `json:"digest"`
	ContentVersion *int       `json:"content_version"`
	Invalidated    *bool      `json:"invalidated"`
	UpdatedAt      *time.Time `json:"updated_at,omitempty"`
}

type DeliveryNativeReview struct {
	ID        string `json:"id"`
	ActorID   string `json:"actor_id"`
	Type      string `json:"type"`
	Commit    string `json:"commit"`
	Content   string `json:"content"`
	Digest    string `json:"digest"`
	Official  *bool  `json:"official"`
	Stale     *bool  `json:"stale"`
	Dismissed bool   `json:"dismissed"`
}

type DeliveryThread struct {
	ProviderID     string                 `json:"provider_id,omitempty"`
	Outdated       *bool                  `json:"outdated,omitempty"`
	OriginalAnchor *ReviewThreadAnchor    `json:"original_anchor"`
	RootID         string                 `json:"root_id"`
	ReviewID       string                 `json:"review_id"`
	Path           string                 `json:"path"`
	Side           string                 `json:"side"`
	Line           int64                  `json:"line"`
	OriginalCommit string                 `json:"original_commit"`
	HeadCommit     string                 `json:"head_commit"`
	Digest         string                 `json:"digest"`
	Resolved       bool                   `json:"resolved"`
	ResolverID     string                 `json:"resolver_id"`
	Members        []DeliveryThreadMember `json:"members"`
}

type DeliveryThreadMember struct {
	ID             string              `json:"id"`
	OriginalAnchor *ReviewThreadAnchor `json:"original_anchor"`
	Resolved       *bool               `json:"resolved"`
}

// ConversationDigest reproduces complete native evidence from exact bodies,
// ordered members and per-comment original context/resolution. resolveRoot
// predicts only the named root's resolution; sibling member state is retained.
func (t DeliveryThread) ConversationDigest(comments []DeliveryComment, resolveRoot bool) (string, error) {
	byID := make(map[string]DeliveryComment, len(comments))
	for _, c := range comments {
		if _, duplicate := byID[c.ID]; duplicate {
			return "", errors.New("duplicate conversation body")
		}
		byID[c.ID] = c
	}
	if t.Path == "" || t.Line < 0 || t.Line == 0 && t.Side != "" || t.Line > 0 && t.Side != "old" && t.Side != "new" {
		return "", errors.New("invalid recorded conversation location")
	}
	seen := map[string]bool{}
	var lines []string
	for i, member := range t.Members {
		c, found := byID[member.ID]
		id, err := strconv.ParseInt(member.ID, 10, 64)
		if !found || err != nil || id <= 0 || strconv.FormatInt(id, 10) != member.ID || seen[member.ID] || c.ContentVersion == nil || *c.ContentVersion < 0 || member.Resolved == nil || c.ReviewID != t.ReviewID {
			return "", errors.New("invalid conversation member")
		}
		seen[member.ID] = true
		if i == 0 && (member.ID != t.RootID || *member.Resolved != t.Resolved || !member.OriginalAnchor.Equal(t.OriginalAnchor)) {
			return "", errors.New("conversation root binding differs")
		}
		if member.OriginalAnchor != nil && member.OriginalAnchor.Validate() != nil {
			return "", errors.New("invalid original conversation context")
		}
		state := "open"
		if *member.Resolved || resolveRoot && i == 0 {
			state = "resolved"
		}
		lines = append(lines, ResolveConversationLine(ResolveConversationMember{ID: id, Body: c.Content, ContentVer: int64(*c.ContentVersion), Location: t.Path + "|" + t.Side + "|" + strconv.FormatInt(t.Line, 10), ResolverState: state, OriginalAnchor: member.OriginalAnchor}))
	}
	return ConversationDigest(lines)
}
