package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
)

type nativeReviewCommentSnapshot struct {
	Count                 uint64 `json:"count"`
	SHA256                string `json:"sha256"`
	IdentityWritingSHA256 string `json:"identity_writing_sha256"`
	Complete              *bool  `json:"complete"`
	Condition             string `json:"condition"`
}

func (s nativeReviewCommentSnapshot) valid() bool {
	return s.Count <= 1000 && nativeCreationDigest(s.SHA256) && nativeCreationDigest(s.IdentityWritingSHA256) && s.Complete != nil && *s.Complete && s.Condition == "bounded_observation"
}

type nativePendingReviewFacts struct {
	ExternalID    *string    `json:"external_id"`
	NodeID        string     `json:"node_id"`
	State         string     `json:"state"`
	Body          string     `json:"body"`
	Commit        *string    `json:"commit"`
	CommentsCount *uint64    `json:"comments_count"`
	CreatedAt     *time.Time `json:"created_at"`
	UpdatedAt     *time.Time `json:"updated_at"`
}
type nativePendingReviewWrite struct {
	Repository       string  `json:"repository"`
	RepositoryNodeID string  `json:"repository_node_id"`
	PullID           string  `json:"pull_id"`
	PullNodeID       string  `json:"pull_node_id"`
	Number           uint64  `json:"number"`
	Head             string  `json:"head"`
	ReviewID         *string `json:"review_id"`
	ReviewNodeID     string  `json:"review_node_id"`
	ReviewCommit     string  `json:"review_commit"`
	PendingSHA256    string  `json:"pending_sha256"`
	CommentsSHA256   string  `json:"comments_sha256"`
	CommentsCount    uint64  `json:"comments_count"`
	Event            string  `json:"event"`
	Body             *string `json:"body"`
}
type nativePendingReviewPreparation struct {
	Version             int                      `json:"version"`
	Account             string                   `json:"account"`
	NotAfter            int64                    `json:"not_after"`
	ActorNodeID         string                   `json:"actor_node_id"`
	EffectiveBodySHA256 string                   `json:"effective_body_sha256"`
	Review              nativePendingReviewWrite `json:"review"`
}
type nativePendingReviewEffect struct {
	Kind      string                   `json:"kind"`
	CommandID string                   `json:"command_id"`
	Digest    string                   `json:"digest"`
	NotAfter  int64                    `json:"not_after"`
	Review    nativePendingReviewWrite `json:"review"`
}
type nativePublishedReviewFacts struct {
	ExternalID    string     `json:"external_id"`
	NodeID        string     `json:"node_id"`
	State         string     `json:"state"`
	BodySHA256    string     `json:"body_sha256"`
	Commit        *string    `json:"commit"`
	CommentsCount *uint64    `json:"comments_count"`
	CreatedAt     *time.Time `json:"created_at"`
	UpdatedAt     *time.Time `json:"updated_at"`
	SubmittedAt   *time.Time `json:"submitted_at"`
}
type nativePendingReviewReceipt struct {
	Account          collaboration.ObservedActor `json:"account"`
	Repository       string                      `json:"repository"`
	RepositoryNodeID string                      `json:"repository_node_id"`
	PullID           string                      `json:"pull_id"`
	PullNodeID       string                      `json:"pull_node_id"`
	Number           uint64                      `json:"number"`
	Head             string                      `json:"head"`
	HeadCondition    string                      `json:"head_condition"`
	Event            string                      `json:"event"`
	BodySource       string                      `json:"body_source"`
	Pending          struct {
		ExternalID *string `json:"external_id"`
		NodeID     string  `json:"node_id"`
		SHA256     string  `json:"sha256"`
	} `json:"pending"`
	ExpectedComments struct {
		Count                 *uint64 `json:"count"`
		IdentityWritingSHA256 string  `json:"identity_writing_sha256"`
	} `json:"expected_comments"`
	Review struct {
		nativePublishedReviewFacts
		AuthorNodeID string `json:"author_node_id"`
	} `json:"review"`
	PublicationVerification string                 `json:"publication_verification"`
	ProviderResponse        nativeCreationResponse `json:"provider_response"`
}
type nativeReviewPublication struct {
	Repository       string `json:"repository"`
	Name             string `json:"name"`
	RepositoryNodeID string `json:"repository_node_id"`
	ActorNodeID      string `json:"actor_node_id"`
	Pull             struct {
		ExternalID string `json:"external_id"`
		NodeID     string `json:"node_id"`
		Number     uint64 `json:"number"`
	} `json:"pull"`
	Review           nativePublishedReviewFacts  `json:"review"`
	CommentsSnapshot nativeReviewCommentSnapshot `json:"comments_snapshot"`
	ObservationOnly  *bool                       `json:"observation_only"`
}
type nativeHumanPendingReviews struct{ *nativeProductAccount }

func (s nativeHumanPendingReviews) PrepareHumanReview(ctx context.Context, q factory.HumanReviewCommand) (factory.HumanReviewCapture, error) {
	return (nativeHumanNewReviews{s.nativeProductAccount}).PrepareHumanReview(ctx, q)
}
func (s nativeHumanPendingReviews) prepare(ctx context.Context, q factory.HumanReviewCommand, hint nativeReviewPreparationFacts) (factory.HumanReviewCapture, error) {
	var empty factory.HumanReviewCapture
	// Draft summary editing needs its own explicit semantics. Submission never
	// silently edits a draft or creates a replacement for the observed record.
	if q.Event == factory.HumanReviewDraft {
		return empty, factoryservice.ErrHumanReviewUnavailable
	}
	var first nativePendingReviewFacts
	if nativeDecode(hint.Pending, &first) != nil {
		return empty, errNativeRead
	}
	var observed nativeReviewPreparationFacts
	if err := s.read(ctx, nativeRead{Kind: "pull_pending_review_snapshot", Repository: s.repository.ExternalID, Number: uint32(q.Number)}, &observed); err != nil {
		return empty, err
	}
	var pending nativePendingReviewFacts
	if observed.Repository != hint.Repository || observed.RepositoryNodeID != hint.RepositoryNodeID || observed.ActorNodeID != hint.ActorNodeID || observed.Pull.ExternalID != hint.Pull.ExternalID || observed.Pull.NodeID != hint.Pull.NodeID || observed.Pull.Number != uint64(q.Number) || observed.Pull.Head != hint.Pull.Head || nativeDecode(observed.Pending, &pending) != nil || pending.NodeID != first.NodeID || !nativeReviewNode(pending.NodeID, 1024) || pending.ExternalID != nil && !nativeRemoteID(*pending.ExternalID) || pending.State != "pending" || len(pending.Body) > 65536 || !utf8.ValidString(pending.Body) || strings.ContainsRune(pending.Body, '\x00') || pending.Commit == nil || !collaboration.ValidCommitID(collaboration.CommitID(*pending.Commit)) || pending.CommentsCount == nil || observed.PendingSHA256 == nil || !nativeCreationDigest(*observed.PendingSHA256) || observed.CommentsSnapshot == nil || !observed.CommentsSnapshot.valid() || observed.CommentsSnapshot.Count != *pending.CommentsCount {
		return empty, errNativeRead
	}
	if _, err := s.mapRead(ctx, observed.Repository, observed.Name, nil, nil); err != nil {
		return empty, err
	}
	body, hash := (*string)(nil), nativeCreationSHA([]byte(pending.Body))
	// The canonical empty summary means preserve the saved pending writing.
	// A future explicit-clear UI command must carry that distinct intent.
	if q.Body != "" {
		text := q.Body
		body, hash = &text, nativeCreationSHA([]byte(text))
	}
	p := nativePendingReviewPreparation{Version: 2, Account: s.identity.Account.ExternalID, NotAfter: time.Now().Add(5 * time.Minute).Unix(), ActorNodeID: observed.ActorNodeID, EffectiveBodySHA256: hash, Review: nativePendingReviewWrite{Repository: observed.Repository, RepositoryNodeID: observed.RepositoryNodeID, PullID: observed.Pull.ExternalID, PullNodeID: observed.Pull.NodeID, Number: uint64(q.Number), Head: observed.Pull.Head, ReviewID: pending.ExternalID, ReviewNodeID: pending.NodeID, ReviewCommit: *pending.Commit, PendingSHA256: *observed.PendingSHA256, CommentsSHA256: observed.CommentsSnapshot.IdentityWritingSHA256, CommentsCount: *pending.CommentsCount, Event: string(q.Event), Body: body}}
	raw, err := json.Marshal(p)
	if err != nil {
		return empty, err
	}
	c := factory.HumanReviewCapture{Request: q, Authority: s.humanAuthority(), OperationID: q.ID, ProviderIntent: raw}
	_, err = c.Command()
	return c, err
}
func (s nativeHumanPendingReviews) decode(ctx context.Context, c factory.HumanReviewCapture) (nativePendingReviewPreparation, error) {
	var p nativePendingReviewPreparation
	if _, err := c.Command(); err != nil {
		return p, factoryservice.ErrHumanReviewQuery
	}
	if err := (nativeHumanNewReviews{s.nativeProductAccount}).admit(ctx, c.Request); err != nil {
		return p, err
	}
	if c.Authority != s.humanAuthority() {
		return p, factoryservice.ErrHumanReviewNamespace
	}
	if c.OperationID != c.Request.ID || nativeDecode(c.ProviderIntent, &p) != nil || p.Version != 2 || p.Account != s.identity.Account.ExternalID || p.NotAfter <= 0 || !nativeReviewNode(p.ActorNodeID, 1024) || !nativeCreationDigest(p.EffectiveBodySHA256) {
		return p, factoryservice.ErrHumanReviewConflict
	}
	w := p.Review
	if w.Repository != s.repository.ExternalID || !nativeReviewNode(w.RepositoryNodeID, 1024) || !nativeRemoteID(w.PullID) || !nativeReviewNode(w.PullNodeID, 1024) || w.Number != uint64(c.Request.Number) || !collaboration.ValidCommitID(collaboration.CommitID(w.Head)) || w.ReviewID != nil && !nativeRemoteID(*w.ReviewID) || !nativeReviewNode(w.ReviewNodeID, 1024) || !collaboration.ValidCommitID(collaboration.CommitID(w.ReviewCommit)) || !nativeCreationDigest(w.PendingSHA256) || !nativeCreationDigest(w.CommentsSHA256) || w.CommentsCount > 1000 || w.Event != string(c.Request.Event) || c.Request.Event == factory.HumanReviewDraft || (w.Body == nil) != (c.Request.Body == "") || w.Body != nil && (*w.Body != c.Request.Body || p.EffectiveBodySHA256 != nativeCreationSHA([]byte(*w.Body))) {
		return p, factoryservice.ErrHumanReviewConflict
	}
	return p, nil
}
func sameNativeReviewID(a, b *string) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}
func validateNativePendingReviewReceipt(c factory.HumanReviewCapture, p nativePendingReviewPreparation, raw json.RawMessage) (nativePendingReviewReceipt, error) {
	var r nativePendingReviewReceipt
	w := p.Review
	state := map[factory.HumanReviewEvent]string{factory.HumanReviewComment: "commented", factory.HumanReviewApprove: "approved", factory.HumanReviewRequestChanges: "changes_requested"}[c.Request.Event]
	source := "unchanged_pending"
	if w.Body != nil {
		source = "explicit"
	}
	if len(raw) > 4096 || nativeDecode(raw, &r) != nil || !nativeActor(r.Account) || r.Account.ExternalID != p.Account || r.Repository != w.Repository || r.RepositoryNodeID != w.RepositoryNodeID || r.PullID != w.PullID || r.PullNodeID != w.PullNodeID || r.Number != w.Number || r.Head != w.Head || r.HeadCondition != "preflight_and_causal_reply" || r.Event != w.Event || r.BodySource != source || !sameNativeReviewID(r.Pending.ExternalID, w.ReviewID) || r.Pending.NodeID != w.ReviewNodeID || r.Pending.SHA256 != w.PendingSHA256 || r.ExpectedComments.Count == nil || *r.ExpectedComments.Count != w.CommentsCount || r.ExpectedComments.IdentityWritingSHA256 != w.CommentsSHA256 || !nativeRemoteID(r.Review.ExternalID) || w.ReviewID != nil && r.Review.ExternalID != *w.ReviewID || r.Review.NodeID != w.ReviewNodeID || r.Review.State != state || r.Review.BodySHA256 != p.EffectiveBodySHA256 || r.Review.Commit == nil || *r.Review.Commit != w.ReviewCommit || r.Review.CommentsCount == nil || r.Review.AuthorNodeID != p.ActorNodeID || r.Review.SubmittedAt == nil || r.PublicationVerification != "required" || r.ProviderResponse.Protocol != "github_graphql_submit_review" || r.ProviderResponse.Status != 200 || !nativeCreationDigest(r.ProviderResponse.ResponseSHA256) || r.ProviderResponse.RequestID != nil && !nativeObservedText(*r.ProviderResponse.RequestID, 256, false) {
		return r, factoryservice.ErrHumanReviewConflict
	}
	return r, nil
}
func (s nativeHumanPendingReviews) LookupHumanReview(ctx context.Context, c factory.HumanReviewCapture) (collaboration.OperationLookup, error) {
	p, err := s.decode(ctx, c)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	o, err := s.humanReviewLookup(ctx, c)
	if err != nil || o.Record == nil || o.Record.Effect != collaboration.EffectCommitted {
		return o, err
	}
	// Authenticate the saved causal primary before a current observation can be
	// used for completion. Never infer it from a current matching review.
	if o.Record.Receipt == nil || o.Record.Receipt.IntentDigest != nativeCreationSHA(c.ProviderIntent) {
		return collaboration.OperationLookup{}, factory.ErrHumanReviewObservation
	}
	r, err := validateNativePendingReviewReceipt(c, p, o.Record.Receipt.Payload)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	if o.Record.Completion == collaboration.CompletionComplete || *r.Review.CommentsCount != p.Review.CommentsCount {
		return o, nil
	}
	var published nativeReviewPublication
	if s.read(ctx, nativeRead{Kind: "pull_review_publication", Repository: p.Review.Repository, Number: uint32(p.Review.Number), ReviewID: r.Review.ExternalID, ReviewNodeID: r.Review.NodeID}, &published) != nil {
		return o, nil
	}
	v, w := published.Review, p.Review
	if published.Repository != w.Repository || published.RepositoryNodeID != w.RepositoryNodeID || published.ActorNodeID != p.ActorNodeID || published.Pull.ExternalID != w.PullID || published.Pull.NodeID != w.PullNodeID || published.Pull.Number != w.Number || published.ObservationOnly == nil || !*published.ObservationOnly || v.ExternalID != r.Review.ExternalID || v.NodeID != w.ReviewNodeID || v.BodySHA256 != p.EffectiveBodySHA256 || v.Commit == nil || *v.Commit != w.ReviewCommit || v.SubmittedAt == nil || v.CommentsCount == nil || *v.CommentsCount != w.CommentsCount || !published.CommentsSnapshot.valid() || published.CommentsSnapshot.Count != w.CommentsCount || published.CommentsSnapshot.IdentityWritingSHA256 != w.CommentsSHA256 {
		return o, nil
	}
	if _, err := s.mapRead(ctx, published.Repository, published.Name, nil, nil); err != nil {
		return o, nil
	}
	next := *o.Record
	next.Completion, next.Reason = collaboration.CompletionComplete, "native_pending_review_publication_verified"
	o.Record = &next
	return o, nil
}
func (s nativeHumanPendingReviews) SubmitHumanReview(ctx context.Context, c factory.HumanReviewCapture) (collaboration.OperationRecord, error) {
	var empty collaboration.OperationRecord
	p, err := s.decode(ctx, c)
	if err != nil {
		return empty, err
	}
	if time.Now().Unix() >= p.NotAfter {
		return empty, factoryservice.ErrHumanReviewAdmission
	}
	won, err := s.store.ReserveHumanReviewDispatch(ctx, c)
	if err != nil {
		return empty, err
	}
	if !won {
		o, err := s.LookupHumanReview(ctx, c)
		if err != nil {
			return empty, err
		}
		if o.Record != nil {
			return *o.Record, nil
		}
		return nativeReviewCreateRecord(c), nil
	}
	cmd, err := c.Command()
	if err != nil {
		return empty, err
	}
	outcome, err := s.callHumanEffect(ctx, nativePendingReviewEffect{Kind: "submit_pending_review", CommandID: c.OperationID, Digest: cmd.Digest, NotAfter: p.NotAfter, Review: p.Review}, cmd, p.Review.Repository, factoryservice.ErrHumanReviewConflict)
	if err != nil {
		return empty, err
	}
	r := nativeReviewCreateRecord(c)
	switch outcome.State {
	case "applied":
		if outcome.Failure != nil {
			return empty, factoryservice.ErrHumanReviewUnavailable
		}
		if _, err := validateNativePendingReviewReceipt(c, p, outcome.Receipt); err != nil {
			return empty, err
		}
		r.Effect, r.Reason = collaboration.EffectCommitted, "native_pending_review_submitted"
		r.Receipt = &collaboration.ProviderReceipt{Authority: c.Authority, Reference: c.OperationID, IntentDigest: nativeCreationSHA(c.ProviderIntent), Payload: outcome.Receipt}
	default:
		if !outcome.applyFailure(&r) {
			return empty, factoryservice.ErrHumanReviewUnavailable
		}
	}
	// Save this immutable causal primary before any publication read. A later
	// lookup may finish completion; failed verification must never resend.
	return r, r.Validate()
}
func (s nativeHumanPendingReviews) CancelHumanReview(ctx context.Context, c factory.HumanReviewCapture) (collaboration.OperationRecord, error) {
	if _, err := s.decode(ctx, c); err != nil {
		return collaboration.OperationRecord{}, err
	}
	o, err := s.store.CancelHumanReviewDispatch(ctx, c)
	if err != nil {
		return collaboration.OperationRecord{}, err
	}
	if o.Record == nil {
		return collaboration.OperationRecord{}, factoryservice.ErrHumanReviewUnavailable
	}
	return *o.Record, nil
}
func (s nativeHumanPendingReviews) ReadCreatedHumanReview(ctx context.Context, c factory.HumanReviewCapture, o collaboration.OperationLookup) (collaboration.ReviewRef, error) {
	var empty collaboration.ReviewRef
	p, err := s.decode(ctx, c)
	if err != nil {
		return empty, err
	}
	if factory.ValidateHumanReviewObservation(c, o) != nil || o.Record == nil || o.Record.Effect != collaboration.EffectCommitted || o.Record.Receipt == nil || o.Record.Receipt.IntentDigest != nativeCreationSHA(c.ProviderIntent) {
		return empty, factoryservice.ErrHumanReviewQuery
	}
	r, err := validateNativePendingReviewReceipt(c, p, o.Record.Receipt.Payload)
	if err != nil {
		return empty, err
	}
	page, err := (nativeCollaborationReads{s.nativeProductAccount}).ReadReviews(ctx, c.Request.Actor, c.Request.Repository, c.Request.Number, factoryservice.ReviewQuery{ReviewID: r.Review.ExternalID, Limit: 1})
	if err != nil {
		return empty, err
	}
	if len(page.Items) != 1 || page.Items[0].ExternalID != r.Review.ExternalID || page.Items[0].Pull.Repository != c.Request.Repository || page.Items[0].Pull.Number != c.Request.Number || page.Items[0].Pull.ExternalID != p.Review.PullID || page.Items[0].Author == nil || page.Items[0].Author.ID != c.Request.Actor {
		return empty, factoryservice.ErrHumanReviewUnavailable
	}
	return collaboration.ReviewRef{Pull: page.Items[0].Pull, ExternalID: page.Items[0].ExternalID}, nil
}
