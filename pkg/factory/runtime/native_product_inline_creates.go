package runtime

import (
	"context"
	"encoding/json"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
)

type nativeInlineCreateWrite struct {
	Repository        string                     `json:"repository"`
	RepositoryNodeID  string                     `json:"repository_node_id"`
	PullID            string                     `json:"pull_id"`
	PullNodeID        string                     `json:"pull_node_id"`
	Number            uint32                     `json:"number"`
	Anchor            factory.ReviewThreadAnchor `json:"anchor"`
	PreparationSHA256 string                     `json:"preparation_sha256"`
	Draft             bool                       `json:"draft"`
	Body              string                     `json:"body"`
}

// Retained for decoding version-2 captures made before the provider restriction
// on immediate publication with an existing pending review was established.
type nativeInlineCreatePending struct {
	NodeID         string  `json:"node_id"`
	ExternalID     *string `json:"external_id"`
	Commit         *string `json:"commit"`
	BodySHA256     string  `json:"body_sha256"`
	CommentsSHA256 string  `json:"comments_sha256"`
	CommentsCount  uint64  `json:"comments_count"`
}
type nativeInlineCreatePreparation struct {
	Version         int                        `json:"version"`
	Account         string                     `json:"account"`
	ActorNodeID     string                     `json:"actor_node_id"`
	NotAfter        int64                      `json:"not_after"`
	SelectionSHA256 string                     `json:"selection_sha256"`
	Pending         *nativeInlineCreatePending `json:"pending"`
	Inline          nativeInlineCreateWrite    `json:"inline"`
}
type nativeInlineCreateEffect struct {
	Kind      string                  `json:"kind"`
	CommandID string                  `json:"command_id"`
	Digest    string                  `json:"digest"`
	NotAfter  int64                   `json:"not_after"`
	Inline    nativeInlineCreateWrite `json:"inline"`
}
type nativeInlineCreateReceipt struct {
	Account            collaboration.ObservedActor `json:"account"`
	Repository         string                      `json:"repository"`
	RepositoryNodeID   string                      `json:"repository_node_id"`
	PullID             string                      `json:"pull_id"`
	PullNodeID         string                      `json:"pull_node_id"`
	Number             uint32                      `json:"number"`
	Head               string                      `json:"head"`
	PreparationSHA256  string                      `json:"preparation_sha256"`
	SelectionSHA256    string                      `json:"selection_sha256"`
	Draft              *bool                       `json:"draft"`
	ReviewNodeID       *string                     `json:"review_node_id"`
	ReviewID           *string                     `json:"review_id"`
	CommentNodeID      string                      `json:"comment_node_id"`
	CommentID          *string                     `json:"comment_id"`
	BodySHA256         string                      `json:"body_sha256"`
	SelectionCondition string                      `json:"selection_condition"`
	ProviderResponse   nativeCreationResponse      `json:"provider_response"`
}

func nativeInlineCreation(c factory.HumanInlineCapture) bool {
	var header struct {
		Version int `json:"version"`
	}
	return json.Unmarshal(c.ProviderIntent, &header) == nil && header.Version == 2
}
func (s nativeHumanInlines) prepareCreation(ctx context.Context, q factory.HumanInlineCommand, f nativeInlinePreparationFacts) (factory.HumanInlineCapture, error) {
	var empty factory.HumanInlineCapture
	// GitHub's immediate comment endpoint refuses a second review while the
	// viewer has a pending review. Never submit or discard that review implicitly.
	if string(f.Pending) != "null" {
		return empty, factoryservice.ErrHumanInlineUnavailable
	}
	if f.Archived == nil || *f.Archived || f.Disabled == nil || *f.Disabled || f.Pull.State != "open" || f.Pull.Locked == nil || *f.Pull.Locked || f.Pull.Merged == nil || *f.Pull.Merged {
		return empty, factoryservice.ErrHumanInlineUnavailable
	}
	p := nativeInlineCreatePreparation{Version: 2, Account: s.identity.Account.ExternalID, ActorNodeID: f.ActorNodeID, NotAfter: time.Now().Add(5 * time.Minute).Unix(), SelectionSHA256: f.SelectionSHA256, Inline: nativeInlineCreateWrite{Repository: f.Repository, RepositoryNodeID: f.RepositoryNodeID, PullID: f.Pull.ExternalID, PullNodeID: f.Pull.NodeID, Number: uint32(q.Number), Anchor: q.Anchor, PreparationSHA256: f.SHA256, Draft: q.Draft, Body: q.Body}}
	if _, err := s.mapRead(ctx, f.Repository, f.Name, nil, nil); err != nil {
		return empty, err
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return empty, err
	}
	c := factory.HumanInlineCapture{Request: q, Authority: s.humanAuthority(), OperationID: q.ID, ProviderIntent: raw}
	_, err = c.Command()
	return c, err
}
func (s nativeHumanInlines) decodeCreation(ctx context.Context, c factory.HumanInlineCapture) (nativeInlineCreatePreparation, error) {
	var p nativeInlineCreatePreparation
	if _, err := c.Command(); err != nil {
		return p, factoryservice.ErrHumanInlineQuery
	}
	if err := s.admit(ctx, c.Request); err != nil {
		return p, err
	}
	if c.Authority != s.humanAuthority() {
		return p, factoryservice.ErrHumanInlineNamespace
	}
	if nativeDecode(c.ProviderIntent, &p) != nil || p.Version != 2 || p.Account != s.identity.Account.ExternalID || !nativeReviewNode(p.ActorNodeID, 1024) || p.NotAfter <= 0 || c.OperationID != c.Request.ID || !nativeCreationDigest(p.SelectionSHA256) {
		return p, factoryservice.ErrHumanInlineConflict
	}
	w := p.Inline
	if w.Repository != s.repository.ExternalID || !nativeReviewNode(w.RepositoryNodeID, 1024) || !nativeRemoteID(w.PullID) || !nativeReviewNode(w.PullNodeID, 1024) || w.Number != uint32(c.Request.Number) || w.Anchor != c.Request.Anchor || w.Draft != c.Request.Draft || w.Body != c.Request.Body || !nativeCreationDigest(w.PreparationSHA256) || w.Anchor.EndLine > 2147483647 || w.Draft && p.Pending != nil {
		return p, factoryservice.ErrHumanInlineConflict
	}
	if p.Pending != nil {
		v := p.Pending
		if !nativeReviewNode(v.NodeID, 1024) || v.ExternalID != nil && !nativeRemoteID(*v.ExternalID) || v.Commit != nil && !collaboration.ValidCommitID(collaboration.CommitID(*v.Commit)) || !nativeCreationDigest(v.BodySHA256) || !nativeCreationDigest(v.CommentsSHA256) || v.CommentsCount > 1000 {
			return p, factoryservice.ErrHumanInlineConflict
		}
	}
	return p, nil
}
func validateNativeInlineCreateReceipt(p nativeInlineCreatePreparation, raw json.RawMessage) (nativeInlineCreateReceipt, error) {
	var r nativeInlineCreateReceipt
	if len(raw) > 4096 || nativeDecode(raw, &r) != nil {
		return r, factoryservice.ErrHumanInlineConflict
	}
	w := p.Inline
	protocol, status := "github_graphql_create_inline_review", 200
	validNode := r.ReviewNodeID != nil && nativeReviewNode(*r.ReviewNodeID, 1024)
	// Existing captures retain the causal REST receipt recorded by the earlier
	// file-comment writer; replay never changes it or sends another effect.
	if !w.Draft && r.ProviderResponse.Protocol == "github_rest_create_review_comment" {
		protocol, status, validNode = "github_rest_create_review_comment", 201, r.ReviewNodeID == nil
	}
	if !nativeActor(r.Account) || r.Account.ExternalID != p.Account || r.Repository != w.Repository || r.RepositoryNodeID != w.RepositoryNodeID || r.PullID != w.PullID || r.PullNodeID != w.PullNodeID || r.Number != w.Number || r.Head != w.Anchor.Head || r.PreparationSHA256 != w.PreparationSHA256 || r.SelectionSHA256 != p.SelectionSHA256 || r.Draft == nil || *r.Draft != w.Draft || !nativeReviewNode(r.CommentNodeID, 1024) || r.CommentID != nil && !nativeRemoteID(*r.CommentID) || r.ReviewID == nil || !nativeRemoteID(*r.ReviewID) || !validNode || r.BodySHA256 != nativeCreationSHA([]byte(w.Body)) || r.SelectionCondition != "bounded_preflight_and_causal_reply" || r.ProviderResponse.Protocol != protocol || r.ProviderResponse.Status != status || !nativeCreationDigest(r.ProviderResponse.ResponseSHA256) || r.ProviderResponse.RequestID != nil && !nativeObservedText(*r.ProviderResponse.RequestID, 256, false) {
		return r, factoryservice.ErrHumanInlineConflict
	}
	return r, nil
}
func (s nativeHumanInlines) lookupCreation(ctx context.Context, c factory.HumanInlineCapture) (collaboration.OperationLookup, error) {
	var empty collaboration.OperationLookup
	p, err := s.decodeCreation(ctx, c)
	if err != nil {
		return empty, err
	}
	cmd, err := c.Command()
	if err != nil {
		return empty, err
	}
	o, err := s.store.LookupHumanCommandDispatch(ctx, cmd)
	if err != nil || o.Record == nil || o.Record.Effect != collaboration.EffectCommitted {
		return o, err
	}
	if o.Record.Receipt == nil || o.Record.Receipt.IntentDigest != nativeCreationSHA(c.ProviderIntent) {
		return empty, factory.ErrHumanInlineObservation
	}
	r, err := validateNativeInlineCreateReceipt(p, o.Record.Receipt.Payload)
	if err != nil {
		return empty, err
	}
	if o.Record.Completion == collaboration.CompletionComplete {
		return o, nil
	}
	w := p.Inline
	if w.Draft {
		var f nativeInlinePreservation
		if s.read(ctx, nativeRead{Kind: "inline_preservation", Repository: w.Repository, Number: w.Number, ReviewNodeID: *r.ReviewNodeID, CommentNodeID: r.CommentNodeID}, &f) != nil {
			return o, nil
		}
		pending, snapshot := f.Pending, f.CommentsSnapshot
		excluded := snapshot.Excluding
		member := excluded.Member
		if f.Repository != w.Repository || f.RepositoryNodeID != w.RepositoryNodeID || f.ActorNodeID != p.ActorNodeID || f.Pull.ExternalID != w.PullID || f.Pull.NodeID != w.PullNodeID || f.Pull.Number != uint64(w.Number) || f.Condition != "bounded_observation" || pending.NodeID != *r.ReviewNodeID || !nativeInlineObservedID(r.ReviewID, pending.ExternalID) || pending.State != "pending" || pending.Body != "" || pending.Commit == nil || *pending.Commit != w.Anchor.Head || pending.CommentsCount == nil || *pending.CommentsCount != 1 || !snapshot.nativeReviewCommentSnapshot.valid() || snapshot.Count != 1 || excluded.NodeID != r.CommentNodeID || excluded.Found == nil || !*excluded.Found || excluded.RemainingCount == nil || *excluded.RemainingCount != 0 || member == nil || member.NodeID != r.CommentNodeID || !nativeInlineObservedID(r.CommentID, member.ExternalID) || member.BodySHA256 != r.BodySHA256 || member.Author == nil || member.Author.Kind != "User" || member.Author.NodeID == nil || *member.Author.NodeID != p.ActorNodeID || member.Commit == nil || *member.Commit != w.Anchor.Head || member.OriginalCommit == nil || *member.OriginalCommit != w.Anchor.Head {
			return o, nil
		}
	} else {
		if r.CommentID == nil {
			return o, nil
		}
		var page struct {
			Repository string                `json:"repository"`
			Name       string                `json:"name"`
			Pull       nativeSubjectRef      `json:"pull"`
			Items      []nativeReviewComment `json:"items"`
			NextPage   int                   `json:"next_page"`
		}
		if s.read(ctx, nativeRead{Kind: "pull_review_comment", Repository: w.Repository, Number: w.Number, CommentID: *r.CommentID}, &page) != nil || page.Repository != w.Repository || page.Pull.Kind != "pull" || page.Pull.ExternalID != w.PullID || page.Pull.Number != int64(w.Number) || len(page.Items) != 1 || page.NextPage != 0 {
			return o, nil
		}
		m := page.Items[0]
		if m.ExternalID != *r.CommentID || m.ReviewID == nil || *m.ReviewID != *r.ReviewID || m.Author == nil || m.Author.ExternalID != p.Account || m.Body == nil || nativeCreationSHA([]byte(*m.Body)) != r.BodySHA256 || m.Commit == nil || string(*m.Commit) != w.Anchor.Head || m.OriginalCommit == nil || string(*m.OriginalCommit) != w.Anchor.Head || m.ReplyTo != nil {
			return o, nil
		}
		if p.Pending != nil {
			var f nativeReviewPreparationFacts
			if s.read(ctx, nativeRead{Kind: "pull_pending_review_snapshot", Repository: w.Repository, Number: w.Number}, &f) != nil {
				return o, nil
			}
			var pending nativePendingReviewFacts
			old := p.Pending
			if f.Repository != w.Repository || f.RepositoryNodeID != w.RepositoryNodeID || f.ActorNodeID != p.ActorNodeID || f.Pull.ExternalID != w.PullID || f.Pull.NodeID != w.PullNodeID || nativeDecode(f.Pending, &pending) != nil || pending.NodeID != old.NodeID || !nativeInlineObservedID(old.ExternalID, pending.ExternalID) || pending.State != "pending" || !sameNativeReviewID(old.Commit, pending.Commit) || nativeCreationSHA([]byte(pending.Body)) != old.BodySHA256 || pending.CommentsCount == nil || *pending.CommentsCount != old.CommentsCount || f.CommentsSnapshot == nil || !f.CommentsSnapshot.valid() || f.CommentsSnapshot.Count != old.CommentsCount || f.CommentsSnapshot.IdentityWritingSHA256 != old.CommentsSHA256 {
				return o, nil
			}
		}
	}
	next := *o.Record
	next.Completion, next.Reason = collaboration.CompletionComplete, "native_inline_creation_verified"
	o.Record = &next
	return o, nil
}
func (s nativeHumanInlines) submitCreation(ctx context.Context, c factory.HumanInlineCapture) (collaboration.OperationRecord, error) {
	var empty collaboration.OperationRecord
	p, err := s.decodeCreation(ctx, c)
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
		o, err := s.lookupCreation(ctx, c)
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
	reply, err := s.callHumanEffect(ctx, nativeInlineCreateEffect{Kind: "create_inline", CommandID: c.OperationID, Digest: cmd.Digest, NotAfter: p.NotAfter, Inline: p.Inline}, cmd, p.Inline.Repository, factoryservice.ErrHumanInlineConflict)
	if err != nil {
		return empty, err
	}
	r := nativeInlineRecord(c)
	if reply.State == "created" {
		if reply.Failure != nil {
			return empty, factoryservice.ErrHumanInlineUnavailable
		}
		if _, err := validateNativeInlineCreateReceipt(p, reply.Receipt); err != nil {
			return empty, err
		}
		r.Effect, r.Reason = collaboration.EffectCommitted, "native_inline_created"
		r.Receipt = &collaboration.ProviderReceipt{Authority: c.Authority, Reference: c.OperationID, IntentDigest: nativeCreationSHA(c.ProviderIntent), Payload: reply.Receipt}
	} else if !reply.applyFailure(&r) {
		return empty, factoryservice.ErrHumanInlineUnavailable
	}
	return r, r.Validate()
}
