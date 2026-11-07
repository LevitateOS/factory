package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
)

type nativeReviewCreateWrite struct {
	Repository       string `json:"repository"`
	RepositoryNodeID string `json:"repository_node_id"`
	PullID           string `json:"pull_id"`
	PullNodeID       string `json:"pull_node_id"`
	Number           uint64 `json:"number"`
	Head             string `json:"head"`
	Base             string `json:"base,omitempty"`
	Event            string `json:"event"`
	Body             string `json:"body"`
}
type nativeReviewCreatePreparation struct {
	Version  int                     `json:"version"`
	Account  string                  `json:"account"`
	NotAfter int64                   `json:"not_after"`
	Review   nativeReviewCreateWrite `json:"review"`
}
type nativeReviewCreateEffect struct {
	Kind      string                  `json:"kind"`
	CommandID string                  `json:"command_id"`
	Digest    string                  `json:"digest"`
	NotAfter  int64                   `json:"not_after"`
	Review    nativeReviewCreateWrite `json:"review"`
}
type nativeReviewCreateReceipt struct {
	Base             string                      `json:"base,omitempty"`
	DiffBase         string                      `json:"diff_base,omitempty"`
	Account          collaboration.ObservedActor `json:"account"`
	Repository       string                      `json:"repository"`
	RepositoryNodeID string                      `json:"repository_node_id"`
	PullID           string                      `json:"pull_id"`
	PullNodeID       string                      `json:"pull_node_id"`
	Number           uint64                      `json:"number"`
	Head             string                      `json:"head"`
	Event            string                      `json:"event"`
	HeadCondition    string                      `json:"head_condition"`
	ExistingPending  string                      `json:"existing_pending"`
	Review           struct {
		ExternalID    string     `json:"external_id"`
		NodeID        string     `json:"node_id"`
		State         string     `json:"state"`
		BodySHA256    string     `json:"body_sha256"`
		Commit        string     `json:"commit"`
		CommentsCount *uint64    `json:"comments_count"`
		AuthorNodeID  string     `json:"author_node_id"`
		CreatedAt     *time.Time `json:"created_at"`
		UpdatedAt     *time.Time `json:"updated_at"`
		SubmittedAt   *time.Time `json:"submitted_at"`
	} `json:"review"`
	ProviderResponse nativeCreationResponse `json:"provider_response"`
}
type nativeReviewPreparationFacts struct {
	ViewerIsAuthor      *bool                        `json:"viewer_is_author"`
	PendingSHA256       *string                      `json:"pending_sha256"`
	CommentsSnapshot    *nativeReviewCommentSnapshot `json:"comments_snapshot"`
	Repository          string                       `json:"repository"`
	Name                string                       `json:"name"`
	RepositoryNodeID    string                       `json:"repository_node_id"`
	ActorNodeID         string                       `json:"actor_node_id"`
	Archived            *bool                        `json:"archived"`
	Disabled            *bool                        `json:"disabled"`
	VisiblePendingCount *uint64                      `json:"visible_pending_count"`
	Pending             json.RawMessage              `json:"pending"`
	Pull                struct {
		ExternalID string `json:"external_id"`
		NodeID     string `json:"node_id"`
		Number     uint64 `json:"number"`
		State      string `json:"state"`
		Locked     *bool  `json:"locked"`
		Draft      *bool  `json:"draft"`
		Merged     *bool  `json:"merged"`
		Head       string `json:"head"`
	} `json:"pull"`
}
type nativeHumanNewReviews struct{ *nativeProductAccount }

func (s nativeHumanNewReviews) admit(ctx context.Context, q factory.HumanReviewCommand) error {
	if q.Validate() != nil || !nativeIndex(q.Number) {
		return factoryservice.ErrHumanReviewQuery
	}
	if q.Host != s.binding.Host.ID || q.Actor != s.binding.Actor.ID || s.repository == nil || q.Repository != s.repository.ID {
		return factoryservice.ErrHumanReviewAdmission
	}
	return s.current(ctx)
}
func (s nativeHumanNewReviews) PrepareHumanReview(ctx context.Context, q factory.HumanReviewCommand) (factory.HumanReviewCapture, error) {
	var empty factory.HumanReviewCapture
	if err := s.admit(ctx, q); err != nil {
		return empty, err
	}
	if err := s.visible(ctx, q.Actor, q.Repository); err != nil {
		return empty, err
	}
	var observed nativeReviewPreparationFacts
	if err := s.read(ctx, nativeRead{Kind: "pull_pending_review_snapshot", Repository: s.repository.ExternalID, Number: uint32(q.Number)}, &observed); err != nil {
		return empty, err
	}
	if observed.Repository != s.repository.ExternalID || !nativeReviewNode(observed.RepositoryNodeID, 1024) || !nativeReviewNode(observed.ActorNodeID, 1024) || !nativeRemoteID(observed.Pull.ExternalID) || !nativeReviewNode(observed.Pull.NodeID, 1024) || observed.Pull.Number != uint64(q.Number) || !collaboration.ValidCommitID(collaboration.CommitID(observed.Pull.Head)) || observed.VisiblePendingCount == nil || *observed.VisiblePendingCount > 1000 || len(observed.Pending) == 0 {
		return empty, errNativeRead
	}
	if (q.Event == factory.HumanReviewApprove || q.Event == factory.HumanReviewRequestChanges) && (observed.ViewerIsAuthor == nil || *observed.ViewerIsAuthor) {
		return empty, factoryservice.ErrHumanReviewUnavailable
	}
	if q.ExpectedHead != "" && string(q.ExpectedHead) != observed.Pull.Head {
		return empty, factoryservice.ErrHumanReviewConflict
	}
	// This adapter creates a new review. Existing pending records require their
	// dedicated submission path and cannot be adopted or overwritten here.
	if string(observed.Pending) != "null" {
		return (nativeHumanPendingReviews{s.nativeProductAccount}).prepare(ctx, q, observed)
	}
	if _, err := s.mapRead(ctx, observed.Repository, observed.Name, nil, nil); err != nil {
		return empty, err
	}
	p := nativeReviewCreatePreparation{Version: 1, Account: s.identity.Account.ExternalID, NotAfter: time.Now().Add(5 * time.Minute).Unix(), Review: nativeReviewCreateWrite{Repository: observed.Repository, RepositoryNodeID: observed.RepositoryNodeID, PullID: observed.Pull.ExternalID, PullNodeID: observed.Pull.NodeID, Number: uint64(q.Number), Head: observed.Pull.Head, Event: string(q.Event), Body: q.Body}}

	raw, err := json.Marshal(p)
	if err != nil {
		return empty, err
	}
	c := factory.HumanReviewCapture{Request: q, Authority: s.humanAuthority(), OperationID: q.ID, ProviderIntent: raw}
	_, err = c.Command()
	return c, err
}
func (s nativeHumanNewReviews) decode(ctx context.Context, c factory.HumanReviewCapture) (nativeReviewCreatePreparation, error) {
	var p nativeReviewCreatePreparation
	if _, err := c.Command(); err != nil {
		return p, factoryservice.ErrHumanReviewQuery
	}
	if err := s.admit(ctx, c.Request); err != nil {
		return p, err
	}
	if c.Authority != s.humanAuthority() {
		return p, factoryservice.ErrHumanReviewNamespace
	}
	if c.OperationID != c.Request.ID || nativeDecode(c.ProviderIntent, &p) != nil || p.Version != 1 || p.Account != s.identity.Account.ExternalID || p.NotAfter <= 0 || p.Review.Repository != s.repository.ExternalID || !nativeReviewNode(p.Review.RepositoryNodeID, 1024) || !nativeReviewNode(p.Review.PullNodeID, 1024) || !nativeRemoteID(p.Review.PullID) || p.Review.Number != uint64(c.Request.Number) || !collaboration.ValidCommitID(collaboration.CommitID(p.Review.Head)) || p.Review.Event != string(c.Request.Event) || p.Review.Body != c.Request.Body {
		return p, factoryservice.ErrHumanReviewConflict
	}

	if c.Request.ExpectedHead != "" && string(c.Request.ExpectedHead) != p.Review.Head {
		return p, factoryservice.ErrHumanReviewConflict
	}
	return p, nil
}
func nativeReviewCreateRecord(c factory.HumanReviewCapture) collaboration.OperationRecord {
	cmd, _ := c.Command()
	return collaboration.OperationRecord{Host: c.Request.Host, Authority: c.Authority, OperationID: c.OperationID, Binding: &collaboration.OperationBinding{Repository: c.Request.Repository, Actor: c.Request.Actor, Kind: collaboration.SubmitReview, Atomicity: collaboration.Atomic, IntentDigest: cmd.Digest}, Effect: collaboration.EffectIndeterminate, Cancellation: collaboration.CancellationNone, Completion: collaboration.CompletionNeedsIntervention, Reason: "dispatch_history_unresolved"}
}
func (s nativeHumanNewReviews) LookupHumanReview(ctx context.Context, c factory.HumanReviewCapture) (collaboration.OperationLookup, error) {
	if _, err := s.decode(ctx, c); err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.nativeProductAccount.humanReviewLookup(ctx, c)
}

// A reserved dispatch or an unattributed current review can never authorize a
// second send. Both preparation versions use the same durable journal fence.
func (s *nativeProductAccount) humanReviewLookup(ctx context.Context, c factory.HumanReviewCapture) (collaboration.OperationLookup, error) {
	cmd, err := c.Command()
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.store.LookupHumanCommandDispatch(ctx, cmd)
}
func validateNativeReviewCreateReceipt(c factory.HumanReviewCapture, p nativeReviewCreatePreparation, raw json.RawMessage) (nativeReviewCreateReceipt, error) {
	var r nativeReviewCreateReceipt
	state := map[factory.HumanReviewEvent]string{factory.HumanReviewDraft: "pending", factory.HumanReviewComment: "commented", factory.HumanReviewApprove: "approved", factory.HumanReviewRequestChanges: "changes_requested"}[c.Request.Event]
	if len(raw) > 4096 || nativeDecode(raw, &r) != nil || !nativeActor(r.Account) || r.Account.ExternalID != p.Account || r.Repository != p.Review.Repository || r.RepositoryNodeID != p.Review.RepositoryNodeID || r.PullID != p.Review.PullID || r.PullNodeID != p.Review.PullNodeID || r.Number != p.Review.Number || r.Head != p.Review.Head || r.Event != p.Review.Event || r.HeadCondition != "preflight_and_causal_reply" || r.ExistingPending != "not_observed_before_send" || !nativeRemoteID(r.Review.ExternalID) || !nativeReviewNode(r.Review.NodeID, 1024) || r.Review.State != state || r.Review.BodySHA256 != nativeCreationSHA([]byte(c.Request.Body)) || r.Review.Commit != p.Review.Head || r.Review.CommentsCount == nil || *r.Review.CommentsCount != 0 || !nativeReviewNode(r.Review.AuthorNodeID, 1024) || (c.Request.Event == factory.HumanReviewDraft) != (r.Review.SubmittedAt == nil) || r.ProviderResponse.Protocol != "github_graphql_create_review" || r.ProviderResponse.Status != 200 || !nativeCreationDigest(r.ProviderResponse.ResponseSHA256) {
		return r, factoryservice.ErrHumanReviewConflict
	}
	if r.Base != p.Review.Base || p.Review.Base != "" && !collaboration.ValidCommitID(collaboration.CommitID(p.Review.Base)) {
		return r, factoryservice.ErrHumanReviewConflict
	}
	if r.DiffBase != "" && (p.Review.Base == "" || !collaboration.ValidCommitID(collaboration.CommitID(r.DiffBase))) {
		return r, factoryservice.ErrHumanReviewConflict
	}
	if r.ProviderResponse.RequestID != nil && !nativeObservedText(*r.ProviderResponse.RequestID, 256, false) {
		return r, factoryservice.ErrHumanReviewConflict
	}
	return r, nil
}

func (s nativeHumanNewReviews) SubmitHumanReview(ctx context.Context, c factory.HumanReviewCapture) (collaboration.OperationRecord, error) {
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
		prior, err := s.LookupHumanReview(ctx, c)
		if err != nil {
			return empty, err
		}
		if prior.Record != nil {
			return *prior.Record, nil
		}
		return nativeReviewCreateRecord(c), nil
	}
	cmd, err := c.Command()
	if err != nil {
		return empty, err
	}
	outcome, err := s.callHumanEffect(ctx, nativeReviewCreateEffect{Kind: "create_review", CommandID: c.OperationID, Digest: cmd.Digest, NotAfter: p.NotAfter, Review: p.Review}, cmd, p.Review.Repository, factoryservice.ErrHumanReviewConflict)
	if err != nil {
		return empty, err
	}
	r := nativeReviewCreateRecord(c)
	switch outcome.State {
	case "created":
		if outcome.Failure != nil {
			return empty, factoryservice.ErrHumanReviewUnavailable
		}
		if _, err := validateNativeReviewCreateReceipt(c, p, outcome.Receipt); err != nil {
			return empty, err
		}
		r.Effect, r.Completion, r.Reason = collaboration.EffectCommitted, collaboration.CompletionComplete, "native_review_created"
		r.Receipt = &collaboration.ProviderReceipt{Authority: c.Authority, Reference: c.OperationID, IntentDigest: nativeCreationSHA(c.ProviderIntent), Payload: outcome.Receipt}
	default:
		if !outcome.applyFailure(&r) {
			return empty, factoryservice.ErrHumanReviewUnavailable
		}
	}
	return r, r.Validate()
}
func (s nativeHumanNewReviews) CancelHumanReview(ctx context.Context, c factory.HumanReviewCapture) (collaboration.OperationRecord, error) {
	if _, err := s.decode(ctx, c); err != nil {
		return collaboration.OperationRecord{}, err
	}
	o, err := s.store.CancelHumanReviewDispatch(ctx, c)
	if err != nil {
		return collaboration.OperationRecord{}, err
	}
	if o.Record == nil {
		return collaboration.OperationRecord{}, errors.New("native review cancellation unavailable")
	}
	return *o.Record, nil
}
func (s nativeHumanNewReviews) ReadCreatedHumanReview(ctx context.Context, c factory.HumanReviewCapture, o collaboration.OperationLookup) (collaboration.ReviewRef, error) {
	var empty collaboration.ReviewRef
	p, err := s.decode(ctx, c)
	if err != nil {
		return empty, err
	}
	if factory.ValidateHumanReviewObservation(c, o) != nil || o.Record == nil || o.Record.Effect != collaboration.EffectCommitted || o.Record.Receipt == nil || o.Record.Receipt.IntentDigest != nativeCreationSHA(c.ProviderIntent) {
		return empty, factoryservice.ErrHumanReviewQuery
	}
	r, err := validateNativeReviewCreateReceipt(c, p, o.Record.Receipt.Payload)
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
