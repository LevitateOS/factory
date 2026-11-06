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

type nativeInlineDraftWrite struct {
	Repository        string                     `json:"repository"`
	RepositoryNodeID  string                     `json:"repository_node_id"`
	PullID            string                     `json:"pull_id"`
	PullNodeID        string                     `json:"pull_node_id"`
	Number            uint64                     `json:"number"`
	Anchor            factory.ReviewThreadAnchor `json:"anchor"`
	ReviewNodeID      string                     `json:"review_node_id"`
	PreparationSHA256 string                     `json:"preparation_sha256"`
	Body              string                     `json:"body"`
}
type nativeInlineDraftEffect struct {
	Kind      string                 `json:"kind"`
	CommandID string                 `json:"command_id"`
	Digest    string                 `json:"digest"`
	NotAfter  int64                  `json:"not_after"`
	Inline    nativeInlineDraftWrite `json:"inline"`
}
type nativeInlinePreparationFacts struct {
	nativeReviewPreparationFacts
	Selection       factory.ReviewThreadAnchor `json:"selection"`
	SelectionSHA256 string                     `json:"selection_sha256"`
	SHA256          string                     `json:"sha256"`
	Diff            json.RawMessage            `json:"diff"`
	Condition       string                     `json:"condition"`
}
type nativeInlineDraftPreparation struct {
	Version         int                    `json:"version"`
	Account         string                 `json:"account"`
	NotAfter        int64                  `json:"not_after"`
	ActorNodeID     string                 `json:"actor_node_id"`
	ReviewID        *string                `json:"review_id"`
	PendingSHA256   string                 `json:"pending_sha256"`
	SummarySHA256   string                 `json:"summary_sha256"`
	CommentsSHA256  string                 `json:"comments_sha256"`
	CommentsCount   uint64                 `json:"comments_count"`
	SelectionSHA256 string                 `json:"selection_sha256"`
	Inline          nativeInlineDraftWrite `json:"inline"`
}
type nativeInlineDraftReceipt struct {
	Account            collaboration.ObservedActor `json:"account"`
	Repository         string                      `json:"repository"`
	RepositoryNodeID   string                      `json:"repository_node_id"`
	PullID             string                      `json:"pull_id"`
	PullNodeID         string                      `json:"pull_node_id"`
	Number             uint64                      `json:"number"`
	Head               string                      `json:"head"`
	PreparationSHA256  string                      `json:"preparation_sha256"`
	SelectionSHA256    string                      `json:"selection_sha256"`
	SelectionCondition string                      `json:"selection_condition"`
	ThreadNodeID       string                      `json:"thread_node_id"`
	Comment            struct {
		NodeID      string     `json:"node_id"`
		ExternalID  *string    `json:"external_id"`
		BodySHA256  string     `json:"body_sha256"`
		State       string     `json:"state"`
		CreatedAt   *time.Time `json:"created_at"`
		UpdatedAt   *time.Time `json:"updated_at"`
		PublishedAt *time.Time `json:"published_at"`
	} `json:"comment"`
	Review struct {
		NodeID        string     `json:"node_id"`
		ExternalID    *string    `json:"external_id"`
		State         string     `json:"state"`
		Commit        string     `json:"commit"`
		BodySHA256    string     `json:"body_sha256"`
		CommentsCount *uint64    `json:"comments_count"`
		SubmittedAt   *time.Time `json:"submitted_at"`
	} `json:"review"`
	ExpectedPending struct {
		SHA256         string  `json:"sha256"`
		BodySHA256     string  `json:"body_sha256"`
		CommentsCount  *uint64 `json:"comments_count"`
		CommentsSHA256 string  `json:"comments_sha256"`
	} `json:"expected_pending"`
	PreservationVerification string                 `json:"preservation_verification"`
	ProviderResponse         nativeCreationResponse `json:"provider_response"`
}
type nativeInlineMember struct {
	NodeID     string  `json:"node_id"`
	ExternalID *string `json:"external_id"`
	BodySHA256 string  `json:"body_sha256"`
	Author     *struct {
		Kind   string  `json:"kind"`
		NodeID *string `json:"node_id"`
		Login  string  `json:"login"`
	} `json:"author"`
	Commit         *string    `json:"commit"`
	OriginalCommit *string    `json:"original_commit"`
	CreatedAt      *time.Time `json:"created_at"`
	UpdatedAt      *time.Time `json:"updated_at"`
}
type nativeInlinePreservation struct {
	// The complete snapshot now has one additional exclusion projection.
	PendingSHA256       *string                  `json:"pending_sha256"` // absent in the current-only reader
	Repository          string                   `json:"repository"`
	Name                string                   `json:"name"`
	RepositoryNodeID    string                   `json:"repository_node_id"`
	ActorNodeID         string                   `json:"actor_node_id"`
	Archived            *bool                    `json:"archived"`
	Disabled            *bool                    `json:"disabled"`
	VisiblePendingCount *uint64                  `json:"visible_pending_count"`
	Pending             nativePendingReviewFacts `json:"pending"`
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
	CommentsSnapshot struct {
		nativeReviewCommentSnapshot
		Excluding struct {
			NodeID                string              `json:"node_id"`
			Found                 *bool               `json:"found"`
			RemainingCount        *uint64             `json:"remaining_count"`
			IdentityWritingSHA256 string              `json:"identity_writing_sha256"`
			Member                *nativeInlineMember `json:"member"`
		} `json:"excluding"`
	} `json:"comments_snapshot"`
	Condition string `json:"condition"`
}
type nativeHumanInlines struct{ *nativeProductAccount }

var _ factoryservice.HumanInlineWriter = nativeHumanInlines{}

// A current optional numeric alias may enrich a previously unknown alias under
// the same already-checked Node identity. It never changes the causal receipt.
func nativeInlineObservedID(expected, current *string) bool {
	return current == nil && expected == nil || current != nil && nativeRemoteID(*current) && (expected == nil || *expected == *current)
}

func (s nativeHumanInlines) admit(ctx context.Context, q factory.HumanInlineCommand) error {
	if q.Validate() != nil || !nativeIndex(q.Number) {
		return factoryservice.ErrHumanInlineQuery
	}
	if q.Host != s.binding.Host.ID || q.Actor != s.binding.Actor.ID || s.repository == nil || q.Repository != s.repository.ID {
		return factoryservice.ErrHumanInlineAdmission
	}
	return s.current(ctx)
}
func (s nativeHumanInlines) PrepareHumanInline(ctx context.Context, q factory.HumanInlineCommand) (factory.HumanInlineCapture, error) {
	var empty factory.HumanInlineCapture
	if err := s.admit(ctx, q); err != nil {
		return empty, err
	}
	if !q.Draft || q.Anchor.EndLine > 2147483647 {
		return empty, factoryservice.ErrHumanInlineUnavailable
	}
	if err := s.visible(ctx, q.Actor, q.Repository); err != nil {
		return empty, err
	}
	observed, err := s.port.InlinePreparation(ctx, s.repository.ExternalID, uint32(q.Number), q.Anchor)
	if err != nil {
		return empty, err
	}
	if !sameNativeAccount(s.identity, observed.NativeIdentity) {
		return empty, errNativeRead
	}
	var facts nativeInlinePreparationFacts
	if nativeDecode(observed.Preparation, &facts) != nil || facts.Repository != s.repository.ExternalID || !nativeReviewNode(facts.RepositoryNodeID, 1024) || !nativeReviewNode(facts.ActorNodeID, 1024) || !nativeRemoteID(facts.Pull.ExternalID) || !nativeReviewNode(facts.Pull.NodeID, 1024) || facts.Pull.Number != uint64(q.Number) || facts.Pull.Head != q.Anchor.Head || facts.Selection != q.Anchor || !nativeCreationDigest(facts.SHA256) || !nativeCreationDigest(facts.SelectionSHA256) || facts.Condition != "bounded_observation" || len(facts.Diff) == 0 || string(facts.Diff) == "null" {
		return empty, errNativeRead
	}
	var pending nativePendingReviewFacts
	if nativeDecode(facts.Pending, &pending) != nil || !nativeReviewNode(pending.NodeID, 1024) || pending.ExternalID != nil && !nativeRemoteID(*pending.ExternalID) || pending.State != "pending" || pending.Commit == nil || *pending.Commit != q.Anchor.Head || len(pending.Body) > 65536 || !utf8.ValidString(pending.Body) || strings.ContainsRune(pending.Body, 0) || pending.CommentsCount == nil || facts.PendingSHA256 == nil || !nativeCreationDigest(*facts.PendingSHA256) || facts.CommentsSnapshot == nil || !facts.CommentsSnapshot.valid() || facts.CommentsSnapshot.Count != *pending.CommentsCount {
		return empty, factoryservice.ErrHumanInlineUnavailable
	}
	if facts.CommentsSnapshot.Count >= 1000 || facts.Archived == nil || *facts.Archived || facts.Disabled == nil || *facts.Disabled || facts.Pull.State != "open" || facts.Pull.Locked == nil || *facts.Pull.Locked || facts.Pull.Merged == nil || *facts.Pull.Merged {
		return empty, factoryservice.ErrHumanInlineUnavailable
	}
	if _, err := s.mapRead(ctx, facts.Repository, facts.Name, nil, nil); err != nil {
		return empty, err
	}
	p := nativeInlineDraftPreparation{Version: 1, Account: s.identity.Account.ExternalID, NotAfter: time.Now().Add(5 * time.Minute).Unix(), ActorNodeID: facts.ActorNodeID, ReviewID: pending.ExternalID, PendingSHA256: *facts.PendingSHA256, SummarySHA256: nativeCreationSHA([]byte(pending.Body)), CommentsSHA256: facts.CommentsSnapshot.IdentityWritingSHA256, CommentsCount: facts.CommentsSnapshot.Count, SelectionSHA256: facts.SelectionSHA256, Inline: nativeInlineDraftWrite{Repository: facts.Repository, RepositoryNodeID: facts.RepositoryNodeID, PullID: facts.Pull.ExternalID, PullNodeID: facts.Pull.NodeID, Number: uint64(q.Number), Anchor: q.Anchor, ReviewNodeID: pending.NodeID, PreparationSHA256: facts.SHA256, Body: q.Body}}
	raw, err := json.Marshal(p)
	if err != nil {
		return empty, err
	}
	c := factory.HumanInlineCapture{Request: q, Authority: s.humanAuthority(), OperationID: q.ID, ProviderIntent: raw}
	_, err = c.Command()
	return c, err
}
func (s nativeHumanInlines) decode(ctx context.Context, c factory.HumanInlineCapture) (nativeInlineDraftPreparation, error) {
	var p nativeInlineDraftPreparation
	if _, err := c.Command(); err != nil {
		return p, factoryservice.ErrHumanInlineQuery
	}
	if err := s.admit(ctx, c.Request); err != nil {
		return p, err
	}
	if c.Authority != s.humanAuthority() {
		return p, factoryservice.ErrHumanInlineNamespace
	}
	w := &p.Inline
	if c.OperationID != c.Request.ID || nativeDecode(c.ProviderIntent, &p) != nil || p.Version != 1 || p.Account != s.identity.Account.ExternalID || p.NotAfter <= 0 || !c.Request.Draft || !nativeReviewNode(p.ActorNodeID, 1024) || p.ReviewID != nil && !nativeRemoteID(*p.ReviewID) || !nativeCreationDigest(p.PendingSHA256) || !nativeCreationDigest(p.SummarySHA256) || !nativeCreationDigest(p.CommentsSHA256) || !nativeCreationDigest(p.SelectionSHA256) || p.CommentsCount >= 1000 || w.Repository != s.repository.ExternalID || !nativeReviewNode(w.RepositoryNodeID, 1024) || !nativeRemoteID(w.PullID) || !nativeReviewNode(w.PullNodeID, 1024) || w.Number != uint64(c.Request.Number) || w.Anchor != c.Request.Anchor || w.Anchor.EndLine > 2147483647 || !nativeReviewNode(w.ReviewNodeID, 1024) || !nativeCreationDigest(w.PreparationSHA256) || w.Body != c.Request.Body {
		return p, factoryservice.ErrHumanInlineConflict
	}
	return p, nil
}
func nativeInlineRecord(c factory.HumanInlineCapture) collaboration.OperationRecord {
	cmd, _ := c.Command()
	return collaboration.OperationRecord{Host: c.Request.Host, Authority: c.Authority, OperationID: c.OperationID, Binding: &collaboration.OperationBinding{Repository: c.Request.Repository, Actor: c.Request.Actor, Kind: collaboration.CreateReviewComment, Atomicity: collaboration.Atomic, IntentDigest: cmd.Digest}, Effect: collaboration.EffectIndeterminate, Cancellation: collaboration.CancellationNone, Completion: collaboration.CompletionNeedsIntervention, Reason: "dispatch_history_unresolved"}
}
func validateNativeInlineReceipt(p nativeInlineDraftPreparation, raw json.RawMessage) (nativeInlineDraftReceipt, error) {
	var r nativeInlineDraftReceipt
	w := p.Inline
	if len(raw) > 4096 || nativeDecode(raw, &r) != nil || !nativeActor(r.Account) || r.Account.ExternalID != p.Account || r.Repository != w.Repository || r.RepositoryNodeID != w.RepositoryNodeID || r.PullID != w.PullID || r.PullNodeID != w.PullNodeID || r.Number != w.Number || r.Head != w.Anchor.Head || r.PreparationSHA256 != w.PreparationSHA256 || r.SelectionSHA256 != p.SelectionSHA256 || r.SelectionCondition != "bounded_preflight_and_causal_reply" || !nativeReviewNode(r.ThreadNodeID, 1024) || !nativeReviewNode(r.Comment.NodeID, 1024) || r.Comment.ExternalID != nil && !nativeRemoteID(*r.Comment.ExternalID) || r.Comment.BodySHA256 != nativeCreationSHA([]byte(w.Body)) || (r.Comment.State != "pending" && r.Comment.State != "submitted") || r.Review.NodeID != w.ReviewNodeID || r.Review.ExternalID != nil && !nativeRemoteID(*r.Review.ExternalID) || p.ReviewID != nil && !sameNativeReviewID(r.Review.ExternalID, p.ReviewID) || r.Review.Commit != w.Anchor.Head || !nativeCreationDigest(r.Review.BodySHA256) || r.Review.CommentsCount == nil || r.ExpectedPending.SHA256 != p.PendingSHA256 || r.ExpectedPending.BodySHA256 != p.SummarySHA256 || r.ExpectedPending.CommentsCount == nil || *r.ExpectedPending.CommentsCount != p.CommentsCount || r.ExpectedPending.CommentsSHA256 != p.CommentsSHA256 || r.PreservationVerification != "required" || r.ProviderResponse.Protocol != "github_graphql_append_pending_thread" || r.ProviderResponse.Status != 200 || !nativeCreationDigest(r.ProviderResponse.ResponseSHA256) || r.ProviderResponse.RequestID != nil && !nativeObservedText(*r.ProviderResponse.RequestID, 256, false) {
		return r, factoryservice.ErrHumanInlineConflict
	}
	switch r.Review.State {
	case "pending", "commented", "approved", "changes_requested", "dismissed":
	default:
		return r, factoryservice.ErrHumanInlineConflict
	}
	return r, nil
}
func (s nativeHumanInlines) LookupHumanInline(ctx context.Context, c factory.HumanInlineCapture) (collaboration.OperationLookup, error) {
	var empty collaboration.OperationLookup
	p, err := s.decode(ctx, c)
	if err != nil {
		return empty, err
	}
	state, err := s.store.HumanInlineDispatchState(ctx, c)
	if err != nil {
		return empty, err
	}
	cmd, err := s.store.FactoryCommand(ctx, c.Request.ID)
	if err != nil {
		return empty, err
	}
	o := collaboration.OperationLookup{Host: c.Request.Host, Authority: c.Authority, OperationID: c.OperationID, State: collaboration.LookupNotObserved, Reason: "local_dispatch_unclaimed"}
	if cmd.Outcome != "" {
		var prior collaboration.OperationLookup
		if json.Unmarshal([]byte(cmd.Outcome), &prior) != nil || factory.ValidateHumanInlineObservation(c, prior) != nil {
			return empty, factory.ErrHumanInlineObservation
		}
		if prior.Record != nil {
			o = prior
		}
	}
	if o.Record == nil && state != factory.DispatchUnclaimed {
		r := nativeInlineRecord(c)
		if state == factory.DispatchLegacyUnknown {
			r.Reason = "legacy_dispatch_unknown"
		}
		o.State, o.Record = collaboration.LookupFound, &r
	}
	if o.Record == nil || o.Record.Effect != collaboration.EffectCommitted {
		return o, nil
	}
	if o.Record.Receipt == nil || o.Record.Receipt.IntentDigest != nativeCreationSHA(c.ProviderIntent) {
		return empty, factory.ErrHumanInlineObservation
	}
	r, err := validateNativeInlineReceipt(p, o.Record.Receipt.Payload)
	if err != nil {
		return empty, err
	}
	// A discrepancy already present in the causal reply cannot be repaired by a
	// later matching current state. Keep the immutable primary and intervention.
	if o.Record.Completion == collaboration.CompletionComplete || r.Comment.State != "pending" || r.Comment.PublishedAt != nil || r.Review.State != "pending" || r.Review.SubmittedAt != nil || r.Review.BodySHA256 != p.SummarySHA256 || *r.Review.CommentsCount != p.CommentsCount+1 {
		return o, nil
	}
	var current nativeInlinePreservation
	if s.read(ctx, nativeRead{Kind: "inline_preservation", Repository: p.Inline.Repository, Number: uint32(p.Inline.Number), ReviewNodeID: p.Inline.ReviewNodeID, CommentNodeID: r.Comment.NodeID}, &current) != nil {
		return o, nil
	}
	snap := current.CommentsSnapshot
	excluded := snap.Excluding
	member := excluded.Member
	pending := current.Pending
	if current.Repository != p.Inline.Repository || current.RepositoryNodeID != p.Inline.RepositoryNodeID || current.ActorNodeID != p.ActorNodeID || current.Pull.ExternalID != p.Inline.PullID || current.Pull.NodeID != p.Inline.PullNodeID || current.Pull.Number != p.Inline.Number || current.Condition != "bounded_observation" || pending.NodeID != p.Inline.ReviewNodeID || !nativeInlineObservedID(r.Review.ExternalID, pending.ExternalID) || pending.State != "pending" || pending.Commit == nil || *pending.Commit != p.Inline.Anchor.Head || nativeCreationSHA([]byte(pending.Body)) != p.SummarySHA256 || pending.CommentsCount == nil || *pending.CommentsCount != p.CommentsCount+1 || !snap.nativeReviewCommentSnapshot.valid() || snap.Count != p.CommentsCount+1 || excluded.NodeID != r.Comment.NodeID || excluded.Found == nil || !*excluded.Found || excluded.RemainingCount == nil || *excluded.RemainingCount != p.CommentsCount || excluded.IdentityWritingSHA256 != p.CommentsSHA256 || member == nil || member.NodeID != r.Comment.NodeID || !nativeInlineObservedID(r.Comment.ExternalID, member.ExternalID) || member.BodySHA256 != r.Comment.BodySHA256 || member.Author == nil || member.Author.Kind != "User" || member.Author.NodeID == nil || *member.Author.NodeID != p.ActorNodeID || member.Commit == nil || *member.Commit != p.Inline.Anchor.Head || member.OriginalCommit == nil || *member.OriginalCommit != p.Inline.Anchor.Head {
		return o, nil
	}
	if _, err := s.mapRead(ctx, current.Repository, current.Name, nil, nil); err != nil {
		return o, nil
	}
	next := *o.Record
	next.Completion, next.Reason = collaboration.CompletionComplete, "native_inline_pending_preservation_verified"
	o.Record = &next
	return o, nil
}
func (s nativeHumanInlines) SubmitHumanInline(ctx context.Context, c factory.HumanInlineCapture) (collaboration.OperationRecord, error) {
	var empty collaboration.OperationRecord
	p, err := s.decode(ctx, c)
	if err != nil {
		return empty, err
	}
	if time.Now().Unix() >= p.NotAfter {
		return empty, factoryservice.ErrHumanInlineAdmission
	}
	won, err := s.store.ReserveHumanInlineDispatch(ctx, c)
	if err != nil {
		return empty, err
	}
	if !won {
		o, err := s.LookupHumanInline(ctx, c)
		if err != nil {
			return empty, err
		}
		if o.Record != nil {
			return *o.Record, nil
		}
		return nativeInlineRecord(c), nil
	}
	cmd, err := c.Command()
	if err != nil {
		return empty, err
	}
	var outcome nativeCreationReply
	scope, err := s.port.call(ctx, nativeInlineDraftEffect{Kind: "append_pending_thread", CommandID: c.OperationID, Digest: cmd.Digest, NotAfter: p.NotAfter, Inline: p.Inline}, &outcome)
	if err != nil {
		return empty, err
	}
	if scope.Connection != s.identity.Connection || scope.Repository != p.Inline.Repository || outcome.CommandID != c.OperationID || outcome.Digest != cmd.Digest {
		return empty, factoryservice.ErrHumanInlineConflict
	}
	r := nativeInlineRecord(c)
	switch outcome.State {
	case "created":
		if outcome.Failure != nil {
			return empty, factoryservice.ErrHumanInlineUnavailable
		}
		if _, err := validateNativeInlineReceipt(p, outcome.Receipt); err != nil {
			return empty, err
		}
		r.Effect, r.Reason = collaboration.EffectCommitted, "native_pending_inline_created"
		r.Receipt = &collaboration.ProviderReceipt{Authority: c.Authority, Reference: c.OperationID, IntentDigest: nativeCreationSHA(c.ProviderIntent), Payload: outcome.Receipt}
	case "not_sent", "indeterminate":
		if string(outcome.Receipt) != "null" || outcome.Failure == nil || !nativeErrorCode(outcome.Failure.Code) || outcome.Failure.Status < 400 || outcome.Failure.Status > 599 {
			return empty, factoryservice.ErrHumanInlineUnavailable
		}
		r.Reason = outcome.Failure.Code
		if outcome.State == "not_sent" {
			r.Effect, r.Completion = collaboration.EffectNotCommitted, collaboration.CompletionComplete
		}
	default:
		return empty, factoryservice.ErrHumanInlineUnavailable
	}
	// No preservation read occurs before the application commits this primary.
	return r, r.Validate()
}
func (s nativeHumanInlines) CancelHumanInline(ctx context.Context, c factory.HumanInlineCapture) (collaboration.OperationRecord, error) {
	if _, err := s.decode(ctx, c); err != nil {
		return collaboration.OperationRecord{}, err
	}
	o, err := s.store.CancelHumanInlineDispatch(ctx, c)
	if err != nil {
		return collaboration.OperationRecord{}, err
	}
	if o.Record == nil {
		return collaboration.OperationRecord{}, factoryservice.ErrHumanInlineUnavailable
	}
	return *o.Record, nil
}
